package main

import (
	"strings"
	"sync"
)

// The tray UI is available in English and Polish. "Automatic" follows the system's display
// language (Windows) or locale (Linux). Command-line output stays in English.

const (
	langAuto = ""
	langEN   = "en"
	langPL   = "pl"
)

var texts = map[string][2]string{ // key: {English, Polish}
	"status.starting": {"Starting…", "Uruchamianie…"},
	"status.active":   {"Sending music to the lights", "Wysyła muzykę do świateł"},
	"status.idle":     {"Idle, waiting for sound", "Bezczynny, czeka na dźwięk"},
	"status.paused":   {"Paused", "Wstrzymano"},
	"status.problem":  {"Problem: ", "Problem: "},

	"pause":           {"Pause", "Wstrzymaj"},
	"pause.tip":       {"Stop sending music to the lights", "Przestań wysyłać muzykę do świateł"},
	"source":          {"Audio source", "Źródło dźwięku"},
	"source.tip":      {"Which output's sound to send", "Dźwięk którego wyjścia wysyłać"},
	"source.default":  {"Default output (follows changes)", "Wyjście domyślne (podąża za zmianami)"},
	"channel":         {"Channel", "Kanał"},
	"channel.tip":     {"AudioReactive sync port set on the lights", "Port synchronizacji AudioReactive ustawiony w światłach"},
	"channel.default": {" (default)", " (domyślny)"},
	"sens":            {"Sensitivity", "Czułość"},
	"sens.tip":        {"How strongly the lights react", "Jak mocno światła reagują"},
	"sens.low":        {"Low", "Niska"},
	"sens.normal":     {"Normal", "Normalna"},
	"sens.high":       {"High", "Wysoka"},
	"sens.veryhigh":   {"Very high", "Bardzo wysoka"},
	"lang":            {"Language", "Język"},
	"lang.auto":       {"Automatic (system)", "Automatycznie (systemowy)"},
	"login":           {"Start at login", "Uruchamiaj przy logowaniu"},
	"login.tip":       {"Start automatically when you log in", "Uruchamiaj automatycznie po zalogowaniu"},
	"log":             {"Open log", "Otwórz dziennik"},
	"log.tip":         {"Show the log file", "Pokaż plik dziennika"},
	"quit":            {"Quit", "Zakończ"},
	"already": {"WLED Audio Sync is already running. Look for its icon in the system tray.",
		"WLED Audio Sync już działa. Jego ikona jest w zasobniku systemowym."},
}

var (
	langMu   sync.RWMutex
	langUsed = langEN
)

// setLanguage applies a setting ("", "en" or "pl"); "" detects the system language.
func setLanguage(setting string) {
	lang := setting
	if lang != langEN && lang != langPL {
		lang = detectLanguage()
	}
	langMu.Lock()
	langUsed = lang
	langMu.Unlock()
}

func detectLanguage() string {
	for _, l := range systemLanguages() {
		l = strings.ToLower(l)
		if strings.HasPrefix(l, "pl") {
			return langPL
		}
		if strings.HasPrefix(l, "en") {
			return langEN
		}
	}
	return langEN
}

// T returns the text for key in the current language.
func T(key string) string {
	langMu.RLock()
	pl := langUsed == langPL
	langMu.RUnlock()
	t, ok := texts[key]
	if !ok {
		return key
	}
	if pl {
		return t[1]
	}
	return t[0]
}
