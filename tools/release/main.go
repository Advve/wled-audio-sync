// Command release checks a version before tagging and writes the GitHub release notes.
//
//	go run ./tools/release --check 1.2.3   fails unless every version field equals 1.2.3 and
//	                                       the README's changelog has an entry for it
//	go run ./tools/release 1.2.3           prints the release notes (Markdown)
//
// Run it from the repository root. CI runs both on version tags.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

func main() {
	args := os.Args[1:]
	check := len(args) == 2 && args[0] == "--check"
	if !(check || len(args) == 1) || !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(args[len(args)-1]) {
		fmt.Fprintln(os.Stderr, "usage: go run ./tools/release [--check] X.Y.Z")
		os.Exit(1)
	}
	v := args[len(args)-1]
	if check {
		if problems := checkVersions(v); len(problems) > 0 {
			for _, p := range problems {
				fmt.Fprintln(os.Stderr, p)
			}
			os.Exit(1)
		}
		fmt.Printf("All versions are %s and the changelog has an entry for it.\n", v)
		return
	}
	entry, ok := changelogEntry(v)
	if !ok {
		fmt.Fprintf(os.Stderr, "README.md has no changelog entry for %s\n", v)
		os.Exit(1)
	}
	fmt.Print(notes(v, entry))
}

func read(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// checkVersions compares every place that holds the version with the wanted one.
func checkVersions(want string) []string {
	var problems []string
	mismatch := func(where, got string) {
		if got != want && got != want+".0" {
			problems = append(problems, fmt.Sprintf("%s has version %q, the tag wants %s", where, got, want))
		}
	}

	if m := regexp.MustCompile(`const version = "([^"]*)"`).FindStringSubmatch(read("version.go")); m != nil {
		mismatch("version.go", m[1])
	} else {
		problems = append(problems, "version.go has no `const version`")
	}

	var res struct {
		Version map[string]map[string]struct {
			Fixed struct {
				File    string `json:"file_version"`
				Product string `json:"product_version"`
			} `json:"fixed"`
			Info map[string]map[string]string `json:"info"`
		} `json:"RT_VERSION"`
	}
	if err := json.Unmarshal([]byte(read("winres/winres.json")), &res); err != nil {
		problems = append(problems, "winres/winres.json: "+err.Error())
	}
	for _, langs := range res.Version {
		for _, vi := range langs {
			mismatch("winres/winres.json file_version", vi.Fixed.File)
			mismatch("winres/winres.json product_version", vi.Fixed.Product)
			for _, info := range vi.Info {
				mismatch("winres/winres.json FileVersion", info["FileVersion"])
				mismatch("winres/winres.json ProductVersion", info["ProductVersion"])
			}
		}
	}
	if len(res.Version) == 0 {
		problems = append(problems, "winres/winres.json has no RT_VERSION")
	}

	for _, p := range problems {
		if strings.HasPrefix(p, "winres/") {
			problems = append(problems, "(after changing winres/winres.json, run `go generate ./...` so the .exe picks it up)")
			break
		}
	}
	if _, ok := changelogEntry(want); !ok {
		problems = append(problems, "README.md has no changelog entry for "+want)
	}
	return problems
}

// changelogEntry finds "- **X.Y.Z** summary" in the README and the indented sub-bullets under it.
// It returns the summary and the sub-bullets (with two spaces of indent removed).
func changelogEntry(v string) ([]string, bool) {
	lines := strings.Split(read("README.md"), "\n")
	marker := "- **" + v + "**"
	for i, line := range lines {
		if !strings.HasPrefix(line, marker) {
			continue
		}
		entry := []string{strings.TrimLeft(strings.TrimPrefix(line, marker), " ,:-")}
		for _, l := range lines[i+1:] {
			if !strings.HasPrefix(l, "  ") {
				break
			}
			l = strings.TrimPrefix(l, "  ")
			if strings.HasPrefix(strings.TrimLeft(l, " "), "- ") {
				entry = append(entry, l) // a (possibly nested) bullet
			} else { // a wrapped line: join it to the summary or bullet above
				entry[len(entry)-1] += " " + strings.TrimSpace(l)
			}
		}
		return entry, true
	}
	return nil, false
}

func notes(v string, entry []string) string {
	// "(first release), with …" becomes "First release, with …"
	summary := entry[0]
	if strings.HasPrefix(summary, "(") {
		summary = strings.Replace(summary[1:], ")", "", 1)
	}
	if summary != "" {
		summary = strings.ToUpper(summary[:1]) + summary[1:]
	}
	var b strings.Builder
	if summary != "" {
		fmt.Fprintf(&b, "**%s**\n\n", summary)
	}
	for _, l := range entry[1:] {
		b.WriteString(l + "\n")
	}
	fmt.Fprintf(&b, `
## Downloads

| System | File |
|---|---|
| Windows 10/11 (64-bit) | `+"`wled-audio-sync.exe`"+` |
| Linux, x86-64 | `+"`wled-audio-sync-linux-amd64`"+` |
| Linux, 64-bit ARM (e.g. Raspberry Pi 4/5) | `+"`wled-audio-sync-linux-arm64`"+` |

Each is a single file; nothing to install.

- **Windows:** put the .exe somewhere permanent (e.g. `+"`%%LOCALAPPDATA%%\\Programs\\WLED Audio Sync\\`"+`) and start it. It isn't code-signed yet, so SmartScreen may warn the first time: **More info → Run anyway**. With Smart App Control switched on, Windows blocks unsigned apps completely.
- **Linux:** `+"`install -m 755 wled-audio-sync-linux-amd64 ~/.local/bin/wled-audio-sync`"+`, then run it. Needs PulseAudio or PipeWire; plain GNOME needs the *AppIndicator and KStatusNotifierItem Support* extension for the tray icon.
- **On your WLED lights:** Usermods → AudioReactive → enabled, sync mode **Receive**, port **11988**; then pick a sound-reactive effect.

Verify a download with `+"`SHA256SUMS.txt`"+`: `+"`sha256sum -c SHA256SUMS.txt --ignore-missing`"+` (Linux) or `+"`Get-FileHash <file>`"+` (PowerShell). `+"`THIRD-PARTY-NOTICES.txt`"+` lists the licenses of the libraries inside the app.

---
WLED Audio Sync %s is an independent project, not affiliated with or endorsed by the WLED project.
`, v)
	return b.String()
}
