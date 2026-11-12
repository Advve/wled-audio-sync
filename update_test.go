package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		candidate, current string
		want               bool
	}{
		{"v1.0.1", "1.0.0", true},
		{"1.1.0", "1.0.9", true},
		{"v2.0.0", "1.10.10", true},
		{"v1.10.0", "1.9.0", true}, // numeric, not text, comparison
		{"v1.0.0", "1.0.0", false},
		{"v0.9.0", "1.0.0", false},
		{"v1.1.0-beta", "1.0.0", false},
		{"nonsense", "1.0.0", false},
		{"v1.1.0", "dev", false},
	} {
		if got := newerVersion(c.candidate, c.current); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", c.candidate, c.current, got, c.want)
		}
	}
}

func signedSums(t *testing.T, files map[string][]byte) (sums, sig []byte, pub string) {
	t.Helper()
	pk, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for name, data := range files {
		h := sha256.Sum256(data)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(h[:]), name)
	}
	sums = []byte(b.String())
	sig = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(sk, sums)) + "\n")
	return sums, sig, base64.StdEncoding.EncodeToString(pk)
}

func TestVerifiedHash(t *testing.T) {
	app := []byte("new program")
	sums, sig, pub := signedSums(t, map[string][]byte{"app.exe": app, "other": []byte("x")})
	want := sha256.Sum256(app)

	got, err := verifiedHash(sums, sig, pub, "app.exe")
	if err != nil || hex.EncodeToString(got) != hex.EncodeToString(want[:]) {
		t.Fatalf("good signature: got %x, %v", got, err)
	}
	if _, err := verifiedHash(sums, sig, pub, "missing.exe"); err == nil {
		t.Error("a file not in SHA256SUMS.txt was accepted")
	}
	tampered := []byte(strings.Replace(string(sums), "app.exe", "app.exf", 1))
	if _, err := verifiedHash(tampered, sig, pub, "app.exf"); err == nil {
		t.Error("a changed SHA256SUMS.txt was accepted")
	}
	_, _, otherPub := signedSums(t, nil)
	if _, err := verifiedHash(sums, sig, otherPub, "app.exe"); err == nil {
		t.Error("a signature from another key was accepted")
	}
	if _, err := verifiedHash(sums, sig, "", "app.exe"); err == nil {
		t.Error("an empty public key was accepted")
	}
}

func TestDownloadFileChecksHash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "program bytes")
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "app.new")
	good := sha256.Sum256([]byte("program bytes"))
	if err := downloadFile(context.Background(), srv.URL, path, good[:]); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "program bytes" {
		t.Fatalf("saved %q", b)
	}
	bad := sha256.Sum256([]byte("something else"))
	if err := downloadFile(context.Background(), srv.URL, path, bad[:]); err == nil {
		t.Fatal("a file with the wrong checksum was accepted")
	}
}

func TestUpdaterCheck(t *testing.T) {
	tag, requests, notModified := "v9.0.0", 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "wled-audio-sync/") {
			t.Errorf("User-Agent %q", r.Header.Get("User-Agent"))
		}
		etag := `"` + tag + `"`
		if r.Header.Get("If-None-Match") == etag {
			notModified++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://example.invalid/r","assets":[{"name":"a","browser_download_url":"https://example.invalid/a"}]}`, tag)
	}))
	defer srv.Close()
	u := &updater{url: srv.URL, current: "1.0.0"}
	ctx := context.Background()

	r, err := u.Check(ctx)
	if err != nil || r == nil || r.Version() != "9.0.0" || r.asset("a") == "" {
		t.Fatalf("first check: %+v, %v", r, err)
	}
	if r, err = u.Check(ctx); err != nil || r == nil || notModified != 1 {
		t.Fatalf("repeat check should reuse the ETag: %+v, %v (304s: %d)", r, err, notModified)
	}
	u.current = "9.0.0"
	u.etag = ""
	if r, err = u.Check(ctx); err != nil || r != nil {
		t.Fatalf("same version should report nothing: %+v, %v", r, err)
	}
	if requests != 3 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestInstallTo(t *testing.T) {
	newProgram := []byte("new program")
	served := newProgram
	sums, sig, pub := signedSums(t, map[string][]byte{"app.exe": newProgram})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/SHA256SUMS.txt":
			w.Write(sums)
		case "/SHA256SUMS.txt.sig":
			w.Write(sig)
		case "/app.exe":
			w.Write(served)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	rel := &release{Tag: "v9.0.0"}
	for _, name := range []string{"SHA256SUMS.txt", "SHA256SUMS.txt.sig", "app.exe"} {
		rel.Assets = append(rel.Assets, struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		}{name, srv.URL + "/" + name})
	}
	exe := filepath.Join(t.TempDir(), "app.exe")
	if err := os.WriteFile(exe, []byte("old program"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// a download that doesn't match the signed checksums leaves the program alone
	served = []byte("tampered program")
	if err := installTo(ctx, rel, "app.exe", exe, pub); err == nil {
		t.Fatal("a tampered download was installed")
	}
	if b, _ := os.ReadFile(exe); string(b) != "old program" {
		t.Fatalf("program changed after a failed install: %q", b)
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatal("a failed install left the .new file behind")
	}

	// so does a signature from another key
	served = newProgram
	_, _, otherPub := signedSums(t, nil)
	if err := installTo(ctx, rel, "app.exe", exe, otherPub); err == nil {
		t.Fatal("installed with a signature from another key")
	}

	if err := installTo(ctx, rel, "app.exe", exe, pub); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new program" {
		t.Fatalf("program after install: %q", b)
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "old program" {
		t.Fatalf("old program after install: %q", b)
	}
}
