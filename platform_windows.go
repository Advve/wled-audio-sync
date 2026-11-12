//go:build windows

package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	runKey  = `Software\Microsoft\Windows\CurrentVersion\Run`
	runName = "WLED Audio Sync"
)

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runName)
	return err == nil
}

// autostartPath returns the program path saved in the startup entry ("" if none).
func autostartPath() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue(runName)
	if err != nil {
		return ""
	}
	return strings.Trim(v, `"`)
}

func setAutostart(enable bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !enable {
		if err := k.DeleteValue(runName); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(runName, `"`+exe+`"`)
}

// enableBroadcast allows sending to broadcast addresses (SO_BROADCAST).
func enableBroadcast(_, _ string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_BROADCAST, 1)
	})
	if err != nil {
		return err
	}
	return serr
}

func openFile(path string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", path).Start()
}

// notify shows a Windows toast notification without waiting for it. An unpackaged app has no
// registered app ID for toasts, so it borrows PowerShell's.
func notify(text string) {
	var x strings.Builder
	x.WriteString(`<toast><visual><binding template="ToastGeneric">`)
	if icon := notifyIcon(); icon != "" {
		x.WriteString(`<image placement="appLogoOverride" src="`)
		_ = xml.EscapeText(&x, []byte(icon))
		x.WriteString(`"/>`)
	}
	x.WriteString(`<text>WLED Audio Sync</text><text>`)
	_ = xml.EscapeText(&x, []byte(text))
	x.WriteString(`</text></binding></visual></toast>`)
	script := `[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
$x = New-Object Windows.Data.Xml.Dom.XmlDocument
$x.LoadXml('` + strings.ReplaceAll(x.String(), "'", "''") + `')
$app = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe'
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($app).Show([Windows.UI.Notifications.ToastNotification]::new($x))`
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(b))
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if cmd.Start() == nil {
		go cmd.Wait()
	}
}

// attachConsole gives the windowless (GUI) build a console for command-line modes:
// the terminal it was started from, or a new window.
func attachConsole() {
	if h, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); err == nil && h != 0 && h != windows.InvalidHandle {
		return // already has a console (e.g. a console build)
	}
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	const attachParentProcess = ^uintptr(0)
	if r, _, _ := kernel32.NewProc("AttachConsole").Call(attachParentProcess); r == 0 {
		kernel32.NewProc("AllocConsole").Call()
	}
	if f, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
	if f, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
		os.Stdin = f
	}
}
