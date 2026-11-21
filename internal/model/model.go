// Package model holds the plain data types shared across Simpel Playtime.
package model

import (
	"strings"
	"time"
)

// Kind says whether a tracked thing is a game or a normal app.
type Kind string

const (
	KindGame Kind = "game"
	KindApp  Kind = "app"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return k == KindGame || k == KindApp }

// Source says how an entry got into the library.
type Source string

const (
	SourceDatabase  Source = "database"  // matched the built in game database
	SourceSteam     Source = "steam"     // found through a Steam app manifest
	SourceEpic      Source = "epic"      // found through an Epic Games manifest
	SourceHeuristic Source = "heuristic" // lives in a known game library folder
	SourceAuto      Source = "auto"      // any other app with a window
	SourceCustom    Source = "custom"    // added by the user through Customise
	SourceManual    Source = "manual"    // created only to hold manual time
)

// Label returns a short human readable label for a source.
func (s Source) Label() string {
	switch s {
	case SourceDatabase:
		return "Database"
	case SourceSteam:
		return "Steam"
	case SourceEpic:
		return "Epic Games"
	case SourceHeuristic:
		return "Game folder"
	case SourceAuto:
		return "Auto"
	case SourceCustom:
		return "Custom"
	case SourceManual:
		return "Manual"
	}
	return string(s)
}

// Matcher types used by custom rules.
const (
	MatchExe    = "exe"    // exe file name anywhere, case insensitive
	MatchPath   = "path"   // exact full exe path, case insensitive
	MatchFolder = "folder" // any exe inside this folder (recursive)
)

// Matcher is a user defined rule that maps processes to an entry.
type Matcher struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	// Title is an optional case insensitive regular expression the main
	// window title must match (only used with MatchExe).
	Title string `json:"title,omitempty"`
}

// Meta is optional extra info about a game, fetched from the internet.
type Meta struct {
	Year        int      `json:"year,omitempty"`
	ReleaseDate string   `json:"releaseDate,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	Developers  []string `json:"developers,omitempty"`
	Publishers  []string `json:"publishers,omitempty"`
	Description string   `json:"description,omitempty"`
}

// Entry is one game or app in the library.
type Entry struct {
	Key        string    `json:"key"`
	Name       string    `json:"name"`
	Kind       Kind      `json:"kind"`
	Source     Source    `json:"source"`
	SteamAppID int       `json:"steamAppId,omitempty"`
	Wiki       string    `json:"wiki,omitempty"`
	ExeName    string    `json:"exeName,omitempty"`
	ExePath    string    `json:"exePath,omitempty"`
	Matchers   []Matcher `json:"matchers,omitempty"`
	Favorite   bool      `json:"favorite,omitempty"`
	Hidden     bool      `json:"hidden,omitempty"`
	Notes      string    `json:"notes,omitempty"`
	Tags       []string  `json:"tags,omitempty"`

	IconFile     string    `json:"iconFile,omitempty"`
	PosterFile   string    `json:"posterFile,omitempty"`
	HeroFile     string    `json:"heroFile,omitempty"`
	CustomPoster bool      `json:"customPoster,omitempty"`
	ArtChecked   time.Time `json:"artChecked,omitempty"`
	IconChecked  time.Time `json:"iconChecked,omitempty"`
	Meta         *Meta     `json:"meta,omitempty"`

	FirstSeen  time.Time `json:"firstSeen"`
	LastPlayed time.Time `json:"lastPlayed,omitempty"`
}

// DisplayName returns the name, falling back to the exe name or key.
func (e Entry) DisplayName() string {
	if strings.TrimSpace(e.Name) != "" {
		return e.Name
	}
	if e.ExeName != "" {
		return e.ExeName
	}
	return e.Key
}

// IsGame is a small helper for the common check.
func (e Entry) IsGame() bool { return e.Kind == KindGame }

// Session is one finished (or checkpointed) stretch of playtime.
type Session struct {
	ID    string    `json:"id"`
	Key   string    `json:"key"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Secs is the counted playtime in seconds. It can be smaller than
	// End-Start when tracking was paused or the user was idle.
	Secs int64 `json:"secs"`
	// Focus is how many of those seconds the app was the foreground window.
	Focus  int64 `json:"focus,omitempty"`
	Manual bool  `json:"manual,omitempty"`
}

// Duration returns the counted playtime as a time.Duration.
func (s Session) Duration() time.Duration { return time.Duration(s.Secs) * time.Second }
