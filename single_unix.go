//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// acquireSingleInstance takes an exclusive lock on a per-user lock file (released automatically
// when the process exits). ok is false if another copy holds it. If the check itself fails, the
// app is allowed to run.
func acquireSingleInstance(name string) (release func(), ok bool) {
	dir := os.Getenv("XDG_RUNTIME_DIR") // per-user, cleared at logout
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("%s-%d.lock", name, os.Getuid())), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}, true
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false
	}
	return func() { f.Close() }, true
}

// showAlreadyRunning tells the user where the running copy is: on the terminal and, when started
// from the desktop, as a notification.
func showAlreadyRunning() {
	fmt.Fprintln(os.Stderr, T("already"))
	_ = exec.Command("notify-send", "--app-name=WLED Audio Sync", "WLED Audio Sync", T("already")).Run()
}
