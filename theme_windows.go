//go:build windows

package main

import (
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const personalizeKey = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`

// darkTaskbar reports whether the taskbar (where the tray icon sits) uses the dark theme.
func darkTaskbar() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, personalizeKey, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer k.Close()
	light, _, err := k.GetIntegerValue("SystemUsesLightTheme")
	return err != nil || light == 0 // missing value = dark (the Windows default)
}

// Dark context menus use uxtheme.dll's undocumented-but-stable exports (by ordinal, Windows 10
// 1809+), the same ones many desktop apps use. If they're missing, menus just stay light.
var (
	uxthemeOnce                      sync.Once
	setPreferredAppMode, flushThemes uintptr // ordinals 135, 136
	refreshColorPolicy               uintptr // ordinal 104
)

func loadUxtheme() {
	uxthemeOnce.Do(func() {
		h, err := windows.LoadLibraryEx("uxtheme.dll", 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
		if err != nil {
			return
		}
		setPreferredAppMode, _ = windows.GetProcAddressByOrdinal(h, 135)
		flushThemes, _ = windows.GetProcAddressByOrdinal(h, 136)
		refreshColorPolicy, _ = windows.GetProcAddressByOrdinal(h, 104)
	})
}

// followSystemMenuTheme makes this app's menus follow the Windows app theme (dark/light).
func followSystemMenuTheme() {
	loadUxtheme()
	if setPreferredAppMode == 0 || flushThemes == 0 {
		return
	}
	const allowDark = 1 // follow the system setting
	syscallN(setPreferredAppMode, allowDark)
	syscallN(flushThemes)
}

// themeChanged re-reads the theme so the next menu opens in the new colours.
func themeChanged() {
	loadUxtheme()
	if refreshColorPolicy != 0 {
		syscallN(refreshColorPolicy)
	}
	if flushThemes != 0 {
		syscallN(flushThemes)
	}
}

func syscallN(fn uintptr, args ...uintptr) { _, _, _ = syscall.SyscallN(fn, args...) }
