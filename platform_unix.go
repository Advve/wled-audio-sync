//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// enableBroadcast allows sending to broadcast addresses (SO_BROADCAST).
func enableBroadcast(_, _ string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	})
	if err != nil {
		return err
	}
	return serr
}

// Autostart uses a freedesktop.org autostart entry, so the tray starts with the desktop session.

func autostartFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "autostart", "wled-audio-sync.desktop")
}

func autostartEnabled() bool {
	_, err := os.Stat(autostartFile())
	return err == nil
}

// autostartPath returns the program path saved in the autostart entry ("" if none).
func autostartPath() string {
	b, err := os.ReadFile(autostartFile())
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if exec, ok := strings.CutPrefix(strings.TrimSpace(line), "Exec="); ok {
			if unq, ok := strings.CutPrefix(exec, `"`); ok {
				unq, _, _ = strings.Cut(unq, `"`)
				return strings.NewReplacer(`\\`, `\`, "\\`", "`", `\$`, `$`).Replace(unq)
			}
			return strings.Fields(exec + " ")[0]
		}
	}
	return ""
}

func setAutostart(enable bool) error {
	if !enable {
		if err := os.Remove(autostartFile()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Desktop Entry quoting: wrap in quotes, escape " ` $ \ inside.
	quoted := `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`).Replace(exe) + `"`
	entry := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=WLED Audio Sync
Comment=Sends this computer's audio to WLED lights
Exec=%s
Terminal=false
X-GNOME-Autostart-enabled=true
`, quoted)
	if err := os.MkdirAll(filepath.Dir(autostartFile()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(autostartFile(), []byte(entry), 0o644)
}

func openFile(path string) error { return exec.Command("xdg-open", path).Start() }

// notify shows a desktop notification without waiting for it.
func notify(text string) {
	cmd := exec.Command("notify-send", "--app-name=WLED Audio Sync", "WLED Audio Sync", text)
	if cmd.Start() == nil {
		go cmd.Wait()
	}
}

func attachConsole() {}
