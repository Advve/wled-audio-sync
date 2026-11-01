package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Settings are saved as JSON in the user's config folder
// (Windows: %APPDATA%\wled-audio-sync, Linux: ~/.config/wled-audio-sync).
type Settings struct {
	Device      string   `json:"device"`                // "" = default output, following changes
	Port        int      `json:"port"`                  // WLED AudioReactive sync port ("channel")
	Gain        float64  `json:"gain"`                  // sensitivity multiplier
	Interface   string   `json:"interface,omitempty"`   // this PC's IP on the network to broadcast on; "" = auto
	Unicast     []string `json:"unicast,omitempty"`     // extra WLED device IPs to send to directly
	Multicast   bool     `json:"multicast,omitempty"`   // also send to WLED's multicast group
	NoBroadcast bool     `json:"noBroadcast,omitempty"` // don't broadcast (unicast/multicast only)
	Language    string   `json:"language,omitempty"`    // "" = follow the system, "en", "pl"
	SquelchDB   float64  `json:"squelchDb"`
	Beat        float64  `json:"beat"`
	FPS         int      `json:"fps"`
}

func defaultSettings() Settings {
	return Settings{Port: defaultPort, Gain: 1, SquelchDB: -60, Beat: 1.5, FPS: 50}
}

func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "wled-audio-sync")
}

func settingsPath() string { return filepath.Join(configDir(), "settings.json") }
func logPath() string      { return filepath.Join(configDir(), "wled-audio-sync.log") }

func loadSettings() Settings {
	s := defaultSettings()
	if b, err := os.ReadFile(settingsPath()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if s.Port <= 0 || s.Port > 65535 {
		s.Port = defaultPort
	}
	if s.Gain <= 0 {
		s.Gain = 1
	}
	if s.Beat <= 0 {
		s.Beat = 1.5
	}
	if s.FPS < 10 || s.FPS > 100 {
		s.FPS = 50
	}
	return s
}

func saveSettings(s Settings) error {
	if err := os.MkdirAll(configDir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath(), b, 0o644)
}
