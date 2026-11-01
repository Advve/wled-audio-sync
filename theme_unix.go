//go:build !windows

package main

import "github.com/godbus/dbus/v5"

// darkTaskbar reads the desktop's colour-scheme preference from the freedesktop settings portal
// (GNOME, KDE, Xfce, ...): 1 = prefer dark, 2 = prefer light, 0 = no preference.
// Without a preference, assume dark: most panels are.
func darkTaskbar() bool {
	conn, err := dbus.SessionBus() // shared connection; not closed
	if err != nil {
		return true
	}
	portal := conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop")
	var v dbus.Variant
	if portal.Call("org.freedesktop.portal.Settings.ReadOne", 0,
		"org.freedesktop.appearance", "color-scheme").Store(&v) != nil {
		// older portals: Read returns the value wrapped in one more variant
		if portal.Call("org.freedesktop.portal.Settings.Read", 0,
			"org.freedesktop.appearance", "color-scheme").Store(&v) != nil {
			return true
		}
		if inner, ok := v.Value().(dbus.Variant); ok {
			v = inner
		}
	}
	scheme, ok := v.Value().(uint32)
	return !ok || scheme != 2
}

// Linux tray menus are drawn by the desktop itself, so they already follow its theme.
func followSystemMenuTheme() {}
func themeChanged()          {}
