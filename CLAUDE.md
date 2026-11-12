# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

**WLED Audio Sync**: a Go tray app (Windows + Linux) that captures the PC's audio output and broadcasts it
in WLED AudioReactive's "audio sync v2" UDP format, so any WLED device with AudioReactive sync mode
*Receive* reacts to it. `examples/synced-lamps/` is a WLED preset/config example (two lamps mirroring each
other, touch-button control); `examples/pole-lamp/` is a second one (a 60-LED vertical strip whose 9
hidden LEDs are skipped via the LED output's *skip* setting, more music modes, playlists). They contain no
code. Module path: `github.com/Advve/wled-audio-sync`.

## Commands

Always build with `CGO_ENABLED=0`: everything is pure Go so it cross-compiles from one machine
(fyne.io/systray only needs cgo on macOS, which isn't a target).

```bash
go vet ./... && go test ./...
GOOS=linux go vet ./...                  # Linux-only files (capture_linux.go, *_unix.go) aren't checked otherwise
go test -run TestAnalyzerSine ./...      # single test
go generate ./...                        # after changing the icon or winres/winres.json: redraws winres/icon_*.png, rebuilds rsrc_windows_amd64.syso

GOOS=windows go build -trimpath -ldflags "-s -w -H=windowsgui" -o dist/wled-audio-sync.exe .
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/wled-audio-sync-linux-amd64 .
GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o dist/wled-audio-sync-linux-arm64 .
```

Release targets are only these three (64-bit; no Windows ARM).

## Releasing (same scheme as the author's RivalGUI repo)

- **The version lives in two places, which must match the tag `vX.Y.Z`:**
  - `version.go` (`const version`, shown by `--version` and logged at start)
  - `winres/winres.json`: four fields. After changing it, run `go generate ./...` so the `.exe` properties match.
- **The changelog is the README's `## Changelog` section,** one `- **X.Y.Z** summary` bullet per version with
  two-space-indented sub-bullets. The release notes are generated from it.
- **`go run ./tools/release --check X.Y.Z`** verifies the version fields and the changelog entry;
  `go run ./tools/release X.Y.Z` prints the release notes. The Downloads table and install notes are a
  template inside that tool.
- **`go run ./tools/notices`** regenerates `THIRD-PARTY-NOTICES.txt` from the modules linked into the
  Windows and Linux builds, plus Go's own license. Run it after any dependency change.
- **CI** (`.github/workflows/build.yml`):
  - **Every push and pull request:** gofmt check, vet for both OSes, tests, a check that
    `THIRD-PARTY-NOTICES.txt` is current, then the three builds.
  - **On a `v*` tag:** it first checks the tag against the version, then publishes "WLED Audio Sync X.Y.Z"
    with the binaries, `SHA256SUMS.txt`, `THIRD-PARTY-NOTICES.txt` and the generated notes.
- **README banner:** `docs/banner.svg`, a hand-written SVG using the icon's gradient (#22d3ee → #a855f7 → #ec4899).

Manual checks without the tray: `--no-tray` (live console meter), `--list` (output devices), `--listen`
(decode packets arriving on the port). To inspect the analysis numerically without disturbing real lights,
run `--no-tray --quiet --no-broadcast --unicast 127.0.0.1 --port 11999` and read packets on 127.0.0.1:11999.

This PC runs an installed copy from `%LOCALAPPDATA%\Programs\WLED Audio Sync\` (also the "Start at login"
target). Always build into `dist/`; that copy is only replaced when asked, and while it runs, a
`--no-tray` test run is refused by the single-instance lock.

On the dev Windows machine, Smart App Control may block freshly built unsigned binaries (including
`go test` executables). Check `HKLM\SYSTEM\CurrentControlSet\Control\CI\Policy\VerifiedAndReputablePolicyState`
(1 = on) before assuming a code problem.

## Architecture

Data flow, all in package `main`:

```
captureLoop (capture_windows.go: WASAPI loopback via go-wca | capture_linux.go: PulseAudio/PipeWire sink monitor via jfreymuth/pulse)
  → Ring (ring.go: last 2048 mono samples, decimates ≥88.2 kHz, records time of last data)
  → Engine.frameLoop (engine.go, fixed FPS, default 50) → Analyzer.Process (analyzer.go + fft.go)
  → encodePacket (packet.go, 44-byte v2 layout) → Sender (network.go)
```

- **Engine** owns settings (mutex), a capture *supervisor* goroutine, and the frame loop.
  - The supervisor restarts `captureLoop` on errors.
  - It also restarts it when the device setting changes (via a cancel func) and when the default output
    changes: `captureLoop` returns nil, and the supervisor reopens.
  - The frame loop rebuilds the `Sender` when network settings change or the LAN IP changes (checked every 10 s).
  - Status changes go to `OnStatus` (tray), every frame to `OnFrame` (console meter).
- **Idle:** after `idleAfter` (1 s) of silence the engine stops sending entirely, so several PCs can coexist.
  WASAPI loopback delivers *no* data during silence, so "Ring age > 250 ms" also means silence.
- **Sending:** **broadcast to the LAN subnet is the default** (`subnetBroadcast` of the chosen interface).
  WLED's native multicast 239.0.0.1 is opt-in (`--multicast`) because routers commonly drop multicast
  between wired PCs and WiFi devices (this was the real-world failure that motivated broadcast). The
  interface comes from `sendFromIP`: `--interface`/settings IP, else `guessLANIP()` (default-route
  trick). SO_BROADCAST is set per-platform in `platform_*.go`.
- **Analyzer tuning is deliberate,** for full-range PC audio (WLED's own processing assumes a small
  microphone):
  - Per-band AGC capped by `bandRefShare` of the loudest band.
  - `floorRemove` subtracts part of each band's and the volume's running average for contrast.
  - Pink-weighted major-peak search.
  - Ranges mimic WLED's sound simulator: bands/volume 0–255, magnitude ≤1000, 0.001 when silent.
  - Tests in `main_test.go` guard band selectivity, beat detection, packet offsets and the icon.
  - When retuning, measure live output (see above) rather than guessing.
- **Tray** (`tray.go`, fyne.io/systray; `systray.Run` must stay on the main goroutine):
  - Every label is registered in `labels` so a language switch relabels the whole menu live.
  - `choiceMenu` handles the background-refreshed audio-device list.
  - Settings changes go through `Engine.Update` + `saveSettings`.
- **Platform split by build tags:**
  - `capture_*`
  - `platform_*`: autostart via HKCU Run key / XDG `.desktop`, SO_BROADCAST, `attachConsole` for CLI modes
    of the `-H=windowsgui` build, open-file
  - `theme_*`: dark/light taskbar detection; dark context menus via uxtheme ordinals 135/136/104
  - `lang_*`: system language
- **i18n** (`i18n.go`): one map, `{English, Polish}` per key; `T(key)`. Language: settings (`""` = system) →
  `setLanguage`. Tray/status text only; CLI output stays English.
- **Icon** is drawn in code (`icon.go`), varying by `State` and dark/light taskbar. ICO on Windows, PNG on Linux.
  The `.exe` resource icon comes from the same renderer via `--write-icons` + go-winres (`go generate`).
- **Settings** (`config.go`): JSON in the user config dir (`%APPDATA%\wled-audio-sync`,
  `~/.config/wled-audio-sync`), plus the log file. Flags explicitly passed on the command line override the
  saved values for that run (`flag.Visit` in `main.go`).
- **Single instance** (`single_*.go`): a named mutex `Local\WLED-Audio-Sync` on Windows, a `flock`ed file in
  `$XDG_RUNTIME_DIR` on Linux. It covers tray and `--no-tray` (two senders would fight); `--list`/`--listen`
  are exempt. It is taken *before* `setupLogFile`, because `os.Create` would otherwise wipe the running
  copy's log. A refused tray launch shows a MessageBox / `notify-send`.
- **Autostart** stores the exe's absolute path; `repairAutostart` re-points it only if the stored path no
  longer exists.
- **Updates** (`update.go`):
  - The check runs 2 min after start, then daily: GitHub `releases/latest` with an ETag.
    `WLED_AUDIO_SYNC_UPDATE_URL` points it at a fake release JSON for testing.
  - One notification per version (`NotifiedVersion`); `SkipVersion`; `NoUpdateCheck` turns off the
    automatic check.
  - **Install:** download `SHA256SUMS.txt` + `.sig`, verify the ed25519 signature against `updatePublicKey`,
    download this OS/arch's asset to `<exe>.new` and check its hash. Then rename `<exe>` → `<exe>.old` and
    `.new` → `<exe>`, start it with `--updated` and quit.
  - `--updated` retries the single-instance lock for up to 10 s; `removeOldExe` cleans up `.old`.
  - Release CI signs `SHA256SUMS.txt` with the `UPDATE_SIGNING_KEY` secret (`tools/release sign`).
    **Every release must carry `SHA256SUMS.txt.sig`,** or users fall back to "Download…".
  - Notifications: `notify()` in `platform_*` (a PowerShell toast on Windows, `notify-send` on Linux).

## WLED example gotchas (`examples/synced-lamps/presets.json`)

- **Button "cycle" presets must use the HTTP form `{"win":"P1=21&P2=27&PL=~"}`, not `{"ps":"21~27~"}`.**
  With the JSON form, WLED applies the next preset with CALL_MODE_NO_NOTIFY, so the other synced lamp never
  hears about it and the lamps drift apart.
- The lamps use a private WLED sync **group 2**, so other WLED devices (default group 1) don't follow them.
  Sync sending also needs `if.sync.send.en: true`.
- Lamp presets don't set segment bounds, so they work on any LED count.

## Project rules

- Do not mention the original lamp's 3D models, their author or that project anywhere in the repo (licensing).
- Keep personal data out of committed files: no real LAN IPs, WiFi names or WLED `cfg.json` backups
  (`.gitignore` covers `*cfg-backup*.json`).
- License: MIT, © 2026 Adve.
