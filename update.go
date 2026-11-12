package main

// Update check and one-click install from GitHub Releases.
//
// The check asks GitHub's API for the latest release (nothing else is sent) a couple of minutes
// after start and then once a day. A newer version shows up as a tray item, with one notification
// per version. Installing downloads the release's file for this system, verifies it against
// SHA256SUMS.txt, whose ed25519 signature (SHA256SUMS.txt.sig, made in CI) must match
// updatePublicKey, swaps the program file and restarts.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	updateRepo = "Advve/wled-audio-sync"

	// updatePublicKey verifies SHA256SUMS.txt.sig (base64 ed25519 public key). The private key is
	// the UPDATE_SIGNING_KEY secret used by CI; `go run ./tools/release keygen` makes a new pair.
	// Empty: one-click install is off and "Install" opens the release page instead.
	updatePublicKey = "AexDjqpZgarVioubvSFmE/5epqH1b8mdcojY7rSq7hA="

	// updateURLEnv points the check at another "latest release" JSON, for testing.
	updateURLEnv = "WLED_AUDIO_SYNC_UPDATE_URL"

	updateFirstCheck = 2 * time.Minute
	updateInterval   = 24 * time.Hour
	maxDownload      = 200 << 20
)

// release is the part of GitHub's release JSON the updater uses.
type release struct {
	Tag    string `json:"tag_name"`
	Page   string `json:"html_url"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

func (r release) asset(name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

// updateAssetName is this system's file in a release ("" if releases have none for it).
func updateAssetName() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "windows/amd64":
		return "wled-audio-sync.exe"
	case "linux/amd64":
		return "wled-audio-sync-linux-amd64"
	case "linux/arm64":
		return "wled-audio-sync-linux-arm64"
	}
	return ""
}

// parseVersion reads "1.2.3" (or "v1.2.3"); ok is false for anything else, e.g. "1.2.3-beta".
func parseVersion(v string) (out [3]int, ok bool) {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// newerVersion reports whether candidate is a later release than current.
func newerVersion(candidate, current string) bool {
	c, ok1 := parseVersion(candidate)
	cur, ok2 := parseVersion(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range c {
		if c[i] != cur[i] {
			return c[i] > cur[i]
		}
	}
	return false
}

var (
	updateClient    = &http.Client{Timeout: 5 * time.Minute}
	updateUserAgent = "wled-audio-sync/" + version
)

func updateGet(ctx context.Context, url string, header map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", updateUserAgent)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return updateClient.Do(req)
}

// updater checks for and installs new releases. The tray reacts to onChange.
type updater struct {
	url, current string

	mu         sync.Mutex
	latest     *release // newest release, if newer than current
	etag       string
	lastCheck  time.Time
	installing bool

	onChange func()      // called after a background check finds a new release
	logError func(error) // background check failures (never shown to the user)
}

func newUpdater() *updater {
	url := "https://api.github.com/repos/" + updateRepo + "/releases/latest"
	if u := os.Getenv(updateURLEnv); u != "" {
		url = u
	}
	return &updater{url: url, current: version, logError: func(err error) { log.Printf("%v", err) }}
}

// Available returns the newer release, if a check found one.
func (u *updater) Available() *release {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.latest
}

// Check asks GitHub for the latest release. It returns the newer release, or nil when this is the
// latest version.
func (u *updater) Check(ctx context.Context) (*release, error) {
	u.mu.Lock()
	etag := u.etag
	u.mu.Unlock()
	header := map[string]string{"Accept": "application/vnd.github+json"}
	if etag != "" {
		header["If-None-Match"] = etag // an unchanged answer doesn't count against the rate limit
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := updateGet(ctx, u.url, header)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	u.mu.Lock()
	defer u.mu.Unlock()
	u.lastCheck = time.Now().Round(0)
	switch resp.StatusCode {
	case http.StatusNotModified:
		return u.latest, nil
	case http.StatusOK:
	default:
		return nil, fmt.Errorf("update check: %s", resp.Status)
	}
	var r release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return nil, fmt.Errorf("update check: %w", err)
	}
	u.etag = resp.Header.Get("ETag")
	u.latest = nil
	if newerVersion(r.Tag, u.current) {
		u.latest = &r
	}
	return u.latest, nil
}

// Run checks shortly after start and then once a day while enabled() is true.
func (u *updater) Run(ctx context.Context, enabled func() bool) {
	wait := time.NewTimer(updateFirstCheck)
	defer wait.Stop()
	select {
	case <-ctx.Done():
		return
	case <-wait.C:
	}
	tick := time.NewTicker(time.Hour) // wall-clock check, so sleep and hibernation don't stretch the day
	defer tick.Stop()
	for {
		u.mu.Lock()
		due := time.Now().Round(0).Sub(u.lastCheck) >= updateInterval
		u.mu.Unlock()
		if due && enabled() {
			before := u.Available()
			r, err := u.Check(ctx)
			if err != nil {
				if u.logError != nil {
					u.logError(err)
				}
			} else if r != nil && (before == nil || before.Tag != r.Tag) && u.onChange != nil {
				u.onChange()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// CanInstall reports whether Install can replace this program in place; if not, the tray offers
// the release page instead.
func (u *updater) CanInstall(r *release) bool {
	if updatePublicKey == "" || updateAssetName() == "" || r.asset(updateAssetName()) == "" ||
		r.asset("SHA256SUMS.txt") == "" || r.asset("SHA256SUMS.txt.sig") == "" {
		return false
	}
	exe, err := executablePath()
	if err != nil {
		return false
	}
	f, err := os.CreateTemp(filepath.Dir(exe), ".update-test-*") // can we write next to the program?
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

// Install downloads and verifies r, then puts it in place of the running program. The caller
// restarts with restartUpdated and quits. On error the program is left as it was.
func (u *updater) Install(ctx context.Context, r *release) (exe string, err error) {
	u.mu.Lock()
	if u.installing {
		u.mu.Unlock()
		return "", errors.New("already installing")
	}
	u.installing = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.installing = false
		u.mu.Unlock()
	}()

	exe, err = executablePath()
	if err != nil {
		return "", err
	}
	return exe, installTo(ctx, r, updateAssetName(), exe, updatePublicKey)
}

// installTo replaces the program at exe with release asset name, after checking it.
func installTo(ctx context.Context, r *release, name, exe, publicKey string) error {
	sums, err := download(ctx, r.asset("SHA256SUMS.txt"), 1<<20)
	if err != nil {
		return err
	}
	sig, err := download(ctx, r.asset("SHA256SUMS.txt.sig"), 4<<10)
	if err != nil {
		return err
	}
	want, err := verifiedHash(sums, sig, publicKey, name)
	if err != nil {
		return err
	}

	newPath, oldPath := exe+".new", exe+".old"
	if err := downloadFile(ctx, r.asset(name), newPath, want); err != nil {
		os.Remove(newPath)
		return err
	}
	os.Remove(oldPath) // a leftover from the last update

	// Windows allows renaming a running program, just not replacing or deleting it.
	if err := os.Rename(exe, oldPath); err != nil {
		os.Remove(newPath)
		return err
	}
	if err := os.Rename(newPath, exe); err != nil {
		os.Rename(oldPath, exe)
		os.Remove(newPath)
		return err
	}
	return nil
}

// verifiedHash checks the signature over the checksum file and returns the SHA-256 listed for name.
func verifiedHash(sums, sig []byte, publicKey, name string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("update: bad public key")
	}
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(key, sums, s) {
		return nil, errors.New("update: SHA256SUMS.txt signature does not match")
	}
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		hash, file, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		if ok && strings.TrimLeft(strings.TrimSpace(file), "*") == name {
			b, err := hex.DecodeString(hash)
			if err != nil || len(b) != sha256.Size {
				break
			}
			return b, nil
		}
	}
	return nil, fmt.Errorf("update: SHA256SUMS.txt has no checksum for %s", name)
}

func download(ctx context.Context, url string, limit int64) ([]byte, error) {
	resp, err := updateGet(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// downloadFile saves url to path (executable) if its SHA-256 equals want.
func downloadFile(ctx context.Context, url, path string, want []byte) error {
	resp, err := updateGet(ctx, url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxDownload))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(h.Sum(nil), want) {
		return errors.New("update: downloaded file does not match SHA256SUMS.txt")
	}
	return nil
}

func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// restartUpdated starts the new program with the same options plus --updated, which waits for
// this copy to exit before taking over.
func restartUpdated(exe string) error {
	args := []string{"--updated"}
	for _, a := range os.Args[1:] {
		if a != "--updated" && a != "-updated" {
			args = append(args, a)
		}
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir, _ = os.Getwd()
	return cmd.Start()
}

// removeOldExe deletes the copy left behind by the last update. The previous process may still be
// exiting, so it retries for a few seconds.
func removeOldExe() {
	exe, err := executablePath()
	if err != nil {
		return
	}
	old := exe + ".old"
	for i := 0; i < 10; i++ {
		if err := os.Remove(old); err == nil || os.IsNotExist(err) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// openURL opens a web page in the default browser.
func openURL(url string) error {
	if !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("not opening %q", url)
	}
	return openFile(url)
}
