// Package settings loads and saves the user settings (settings.json).
package settings

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/harsiz/SimpelPlaytime/internal/fsutil"
)

// Track modes.
const (
	TrackWindowed = "windowed" // games + any app that has a visible window
	TrackAll      = "all"      // games + literally every exe (except Windows itself)
	TrackGames    = "games"    // only things recognised as games
)

// Time formats.
const (
	TimeHM      = "hm"      // 27h 12m
	TimeDecimal = "decimal" // 27.2 h
)

// Settings is everything the user can change.
type Settings struct {
	Version int `json:"version"`

	// General
	StartWithWindows     bool `json:"startWithWindows"`
	StartMinimized       bool `json:"startMinimized"`
	CloseToTray          bool `json:"closeToTray"`
	FreeMemoryWhenHidden bool `json:"freeMemoryWhenHidden"`

	// Tracking
	TrackMode         string   `json:"trackMode"`
	PollSeconds       int      `json:"pollSeconds"`
	IdlePauseApps     bool     `json:"idlePauseApps"`
	IdlePauseGames    bool     `json:"idlePauseGames"`
	IdleMinutes       int      `json:"idleMinutes"`
	MinSessionSeconds int      `json:"minSessionSeconds"`
	MergeGapSeconds   int      `json:"mergeGapSeconds"`
	Ignored           []string `json:"ignored"`

	// Artwork
	DownloadArt    bool   `json:"downloadArt"`
	SteamGridDBKey string `json:"steamGridDbKey,omitempty"`

	// Local API
	APIEnabled      bool   `json:"apiEnabled"`
	APIPort         int    `json:"apiPort"`
	APIRequireToken bool   `json:"apiRequireToken"`
	APIToken        string `json:"apiToken"`
	APIAllowCORS    bool   `json:"apiAllowCors"`

	// Notifications
	NotifySessionEnd     bool `json:"notifySessionEnd"`
	NotifyNewGame        bool `json:"notifyNewGame"`
	BreakReminderMinutes int  `json:"breakReminderMinutes"`

	// Display
	WeekStartsMonday bool   `json:"weekStartsMonday"`
	TimeFormat       string `json:"timeFormat"`
	ShowHiddenApps   bool   `json:"showHiddenApps"`
}

// CurrentVersion of the settings file layout.
const CurrentVersion = 1

// DefaultAPIPort is the port the local API listens on by default.
const DefaultAPIPort = 47123

// Defaults returns fresh default settings.
func Defaults() Settings {
	return Settings{
		Version:              CurrentVersion,
		StartWithWindows:     true,
		StartMinimized:       true,
		CloseToTray:          true,
		FreeMemoryWhenHidden: true,
		TrackMode:            TrackWindowed,
		PollSeconds:          5,
		IdlePauseApps:        true,
		IdlePauseGames:       false,
		IdleMinutes:          5,
		MinSessionSeconds:    30,
		MergeGapSeconds:      120,
		Ignored:              []string{},
		DownloadArt:          true,
		APIEnabled:           true,
		APIPort:              DefaultAPIPort,
		APIRequireToken:      false,
		APIToken:             NewToken(),
		APIAllowCORS:         false,
		NotifySessionEnd:     false,
		NotifyNewGame:        true,
		BreakReminderMinutes: 0,
		WeekStartsMonday:     true,
		TimeFormat:           TimeHM,
	}
}

// NewToken makes a random API token.
func NewToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "simpel-playtime"
	}
	return hex.EncodeToString(b)
}

// Normalize clamps values into sane ranges and fills blanks.
func (s *Settings) Normalize() {
	switch s.TrackMode {
	case TrackWindowed, TrackAll, TrackGames:
	default:
		s.TrackMode = TrackWindowed
	}
	s.PollSeconds = clamp(s.PollSeconds, 1, 60, 5)
	s.IdleMinutes = clamp(s.IdleMinutes, 1, 240, 5)
	s.MinSessionSeconds = clamp(s.MinSessionSeconds, 0, 3600, 30)
	s.MergeGapSeconds = clamp(s.MergeGapSeconds, 0, 3600, 120)
	if s.APIPort < 1024 || s.APIPort > 65535 {
		s.APIPort = DefaultAPIPort
	}
	if s.APIToken == "" {
		s.APIToken = NewToken()
	}
	if s.BreakReminderMinutes < 0 {
		s.BreakReminderMinutes = 0
	}
	switch s.TimeFormat {
	case TimeHM, TimeDecimal:
	default:
		s.TimeFormat = TimeHM
	}
	s.Ignored = cleanList(s.Ignored)
	s.Version = CurrentVersion
}

func clamp(v, lo, hi, def int) int {
	if v == 0 && lo > 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func cleanList(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		k := strings.ToLower(s)
		if s == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// IsIgnored reports whether an exe name is on the user ignore list.
func (s Settings) IsIgnored(exe string) bool {
	for _, i := range s.Ignored {
		if strings.EqualFold(i, exe) {
			return true
		}
	}
	return false
}

// Manager holds the live settings and saves them to disk.
type Manager struct {
	mu        sync.RWMutex
	path      string
	cur       Settings
	listeners []func(old, new Settings)
}

// Load reads settings from dir/settings.json, using defaults when missing.
func Load(dir string) (*Manager, error) {
	m := &Manager{path: filepath.Join(dir, "settings.json"), cur: Defaults()}
	data, err := os.ReadFile(m.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		m.cur.Normalize()
		return m, m.save()
	case err != nil:
		return nil, err
	}
	s := Defaults()
	if err := json.Unmarshal(data, &s); err != nil {
		// A broken settings file should not brick the app. Keep a copy and
		// start fresh.
		_ = os.WriteFile(m.path+".broken", data, 0o600)
		s = Defaults()
	}
	s.Normalize()
	m.cur = s
	return m, m.save()
}

// Get returns a copy of the current settings.
func (m *Manager) Get() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.cur
	s.Ignored = append([]string(nil), m.cur.Ignored...)
	return s
}

// Update changes settings through fn, normalizes and saves them, then tells
// listeners.
func (m *Manager) Update(fn func(*Settings)) error {
	m.mu.Lock()
	old := m.cur
	old.Ignored = append([]string(nil), m.cur.Ignored...)
	next := old
	next.Ignored = append([]string(nil), old.Ignored...)
	fn(&next)
	next.Normalize()
	m.cur = next
	err := m.save()
	ls := append([]func(old, new Settings){}, m.listeners...)
	m.mu.Unlock()
	for _, l := range ls {
		l(old, next)
	}
	return err
}

// OnChange registers a listener called after every Update.
func (m *Manager) OnChange(fn func(old, new Settings)) {
	m.mu.Lock()
	m.listeners = append(m.listeners, fn)
	m.mu.Unlock()
}

// Path returns where the settings file lives.
func (m *Manager) Path() string { return m.path }

func (m *Manager) save() error {
	data, err := json.MarshalIndent(m.cur, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(m.path, data, 0o600)
}
