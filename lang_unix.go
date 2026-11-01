//go:build !windows

package main

import (
	"os"
	"strings"
)

// systemLanguages reads the locale environment the usual way: LANGUAGE (a preference list),
// then LC_ALL, LC_MESSAGES, LANG, e.g. "pl_PL.UTF-8".
func systemLanguages() []string {
	var langs []string
	if v := os.Getenv("LANGUAGE"); v != "" {
		langs = append(langs, strings.Split(v, ":")...)
	}
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(name); v != "" && v != "C" && v != "POSIX" {
			langs = append(langs, v)
		}
	}
	return langs
}
