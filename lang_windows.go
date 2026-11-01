//go:build windows

package main

import "golang.org/x/sys/windows"

// systemLanguages returns the user's display languages in order of preference, e.g. ["pl-PL"].
func systemLanguages() []string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil {
		return nil
	}
	return langs
}
