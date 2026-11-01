//go:build windows

package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

// acquireSingleInstance takes a per-user-session named mutex. ok is false if another copy holds
// it. If the check itself fails, the app is allowed to run.
func acquireSingleInstance(name string) (release func(), ok bool) {
	n, err := windows.UTF16PtrFromString(`Local\` + name)
	if err != nil {
		return func() {}, true
	}
	h, err := windows.CreateMutex(nil, false, n)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, false
	}
	if err != nil {
		return func() {}, true
	}
	return func() { windows.CloseHandle(h) }, true
}

// showAlreadyRunning tells the user (who just double-clicked the app) where the running copy is.
func showAlreadyRunning() {
	text, _ := windows.UTF16PtrFromString(T("already"))
	caption, _ := windows.UTF16PtrFromString("WLED Audio Sync")
	const mbIconInformation, mbSetForeground, mbTopmost = 0x40, 0x10000, 0x40000
	_, _ = windows.MessageBox(0, text, caption, mbIconInformation|mbSetForeground|mbTopmost)
}
