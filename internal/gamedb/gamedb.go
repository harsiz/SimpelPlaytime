// Package gamedb is the offline database of known games and apps.
//
// The database is a plain text file (games.txt) that gets embedded into the
// binary. Each line looks like:
//
//	Name | exe1.exe, exe2.exe{title:^Regex}, exe3.exe{path:\some folder\} | steam=123 | wiki=Article
//
// Section lines "@games" and "@apps" switch the kind of the following
// entries. Exe names may contain a "*" glob. Conditions inside braces are
// separated by ";" and can be "title:<regex>" (main window title, case
// insensitive) or "path:<text>" (the full exe path must contain the text,
// case insensitive, use backslashes).
//
// Hash comparisons are not used on purpose: every game patch changes the exe
// hash, so a hash list would go stale within weeks. Exe name plus folder plus
// window title fingerprints survive updates.
package gamedb

import (
	"bufio"
	_ "embed"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/harsiz/SimpelPlaytime/internal/model"
)

//go:embed games.txt
var embedded string

// Rule is one way to recognise an entry.
type Rule struct {
	Exe   string         // lower case exe name, may contain "*"
	Title *regexp.Regexp // optional window title condition
	Path  string         // optional lower case path substring (backslashes)
	entry *Entry
}

// Conditional reports whether the rule has extra conditions.
func (r *Rule) Conditional() bool { return r.Title != nil || r.Path != "" }

// Entry is a known game or app.
type Entry struct {
	ID      string
	Name    string
	Kind    model.Kind
	SteamID int
	Wiki    string
	Rules   []*Rule
}

// Exes returns the exe names of the entry (for display).
func (e *Entry) Exes() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range e.Rules {
		if !seen[r.Exe] {
			seen[r.Exe] = true
			out = append(out, r.Exe)
		}
	}
	return out
}

// DB is a parsed database.
type DB struct {
	entries []*Entry
	byID    map[string]*Entry
	bySteam map[int]*Entry
	byExe   map[string][]*Rule
	globs   []*Rule
}

var (
	defaultOnce sync.Once
	defaultDB   *DB
	defaultErr  error
)

// Default returns the embedded database. It is parsed once.
func Default() (*DB, error) {
	defaultOnce.Do(func() {
		defaultDB, defaultErr = Parse(strings.NewReader(embedded))
	})
	return defaultDB, defaultErr
}

// MustDefault is Default but panics on error (the embedded file is tested).
func MustDefault() *DB {
	db, err := Default()
	if err != nil {
		panic(err)
	}
	return db
}

// Parse reads a database in the games.txt format.
func Parse(r io.Reader) (*DB, error) {
	db := &DB{
		byID:    map[string]*Entry{},
		bySteam: map[int]*Entry{},
		byExe:   map[string][]*Rule{},
	}
	kind := model.KindGame
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "@") {
			switch strings.ToLower(line) {
			case "@games":
				kind = model.KindGame
			case "@apps":
				kind = model.KindApp
			default:
				return nil, fmt.Errorf("line %d: unknown section %q", lineNo, line)
			}
			continue
		}
		e, err := parseLine(line, kind)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		if _, dup := db.byID[e.ID]; dup {
			return nil, fmt.Errorf("line %d: duplicate id %q", lineNo, e.ID)
		}
		db.byID[e.ID] = e
		db.entries = append(db.entries, e)
		if e.SteamID != 0 {
			if other, dup := db.bySteam[e.SteamID]; dup {
				return nil, fmt.Errorf("line %d: steam id %d already used by %q", lineNo, e.SteamID, other.Name)
			}
			db.bySteam[e.SteamID] = e
		}
		for _, rule := range e.Rules {
			if strings.Contains(rule.Exe, "*") {
				db.globs = append(db.globs, rule)
				continue
			}
			db.byExe[rule.Exe] = append(db.byExe[rule.Exe], rule)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// Conditional rules must be tried before plain ones for the same exe.
	for exe, rules := range db.byExe {
		sort.SliceStable(rules, func(i, j int) bool {
			return rules[i].Conditional() && !rules[j].Conditional()
		})
		db.byExe[exe] = rules
	}
	sort.SliceStable(db.globs, func(i, j int) bool {
		return db.globs[i].Conditional() && !db.globs[j].Conditional()
	})
	return db, nil
}

func parseLine(line string, kind model.Kind) (*Entry, error) {
	fields := strings.Split(line, "|")
	if len(fields) < 2 {
		return nil, fmt.Errorf("expected at least 'name | exes'")
	}
	e := &Entry{Name: strings.TrimSpace(fields[0]), Kind: kind}
	if e.Name == "" {
		return nil, fmt.Errorf("empty name")
	}
	for _, opt := range fields[2:] {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			continue
		}
		k, v, ok := strings.Cut(opt, "=")
		if !ok {
			return nil, fmt.Errorf("bad option %q", opt)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "steam":
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("bad steam id %q", v)
			}
			e.SteamID = n
		case "wiki":
			e.Wiki = v
		case "id":
			e.ID = v
		default:
			return nil, fmt.Errorf("unknown option %q", k)
		}
	}
	if e.ID == "" {
		e.ID = Slug(e.Name)
	}
	if e.ID == "" {
		return nil, fmt.Errorf("could not make an id from %q", e.Name)
	}
	tokens, err := splitTokens(fields[1])
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("no exe names for %q", e.Name)
	}
	for _, tok := range tokens {
		rule, err := parseRule(tok)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name, err)
		}
		rule.entry = e
		e.Rules = append(e.Rules, rule)
	}
	return e, nil
}

// splitTokens splits on commas that are not inside braces.
func splitTokens(s string) ([]string, error) {
	var out []string
	depth := 0
	start := 0
	for i, c := range s {
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unbalanced braces in %q", s)
			}
		case ',':
			if depth == 0 {
				if t := strings.TrimSpace(s[start:i]); t != "" {
					out = append(out, t)
				}
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("unbalanced braces in %q", s)
	}
	if t := strings.TrimSpace(s[start:]); t != "" {
		out = append(out, t)
	}
	return out, nil
}

func parseRule(tok string) (*Rule, error) {
	r := &Rule{}
	name := tok
	if i := strings.IndexByte(tok, '{'); i >= 0 {
		if !strings.HasSuffix(tok, "}") {
			return nil, fmt.Errorf("bad rule %q", tok)
		}
		name = strings.TrimSpace(tok[:i])
		conds := tok[i+1 : len(tok)-1]
		for _, c := range strings.Split(conds, ";") {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			k, v, ok := strings.Cut(c, ":")
			if !ok {
				return nil, fmt.Errorf("bad condition %q", c)
			}
			switch strings.TrimSpace(k) {
			case "title":
				re, err := regexp.Compile("(?i)" + v)
				if err != nil {
					return nil, fmt.Errorf("bad title regex %q: %w", v, err)
				}
				r.Title = re
			case "path":
				r.Path = NormPath(v)
			default:
				return nil, fmt.Errorf("unknown condition %q", k)
			}
		}
	}
	r.Exe = strings.ToLower(strings.TrimSpace(name))
	if r.Exe == "" {
		return nil, fmt.Errorf("empty exe in %q", tok)
	}
	if !strings.HasSuffix(r.Exe, ".exe") {
		return nil, fmt.Errorf("exe %q must end in .exe", r.Exe)
	}
	return r, nil
}

// NormPath lower cases a path and turns forward slashes into backslashes so
// path conditions work the same on every OS.
func NormPath(p string) string {
	return strings.ToLower(strings.ReplaceAll(p, "/", `\`))
}

// Slug turns a name into a stable id ("Garry's Mod" -> "garrys-mod").
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			dash = false
		case r == '\'' || r == '’' || r == '.' || r == '!':
			// drop quietly so "osu!" -> "osu" and "garry's" -> "garrys"
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func (r *Rule) matches(exe, normPath, title string) bool {
	if strings.Contains(r.Exe, "*") {
		ok, _ := path.Match(r.Exe, exe)
		if !ok {
			return false
		}
	} else if r.Exe != exe {
		return false
	}
	if r.Path != "" && !strings.Contains(normPath, r.Path) {
		return false
	}
	if r.Title != nil && !r.Title.MatchString(title) {
		return false
	}
	return true
}

// Match finds the entry for a process. exe is the exe file name, fullPath is
// the full exe path (may be empty) and title is the main window title (may be
// empty).
func (db *DB) Match(exe, fullPath, title string) *Entry {
	exe = strings.ToLower(exe)
	np := NormPath(fullPath)
	for _, r := range db.byExe[exe] {
		if r.matches(exe, np, title) {
			return r.entry
		}
	}
	for _, r := range db.globs {
		if r.matches(exe, np, title) {
			return r.entry
		}
	}
	return nil
}

// NeedsTitle reports whether matching this exe depends on the window title.
// The scanner uses it to only fetch titles when they matter.
func (db *DB) NeedsTitle(exe string) bool {
	exe = strings.ToLower(exe)
	for _, r := range db.byExe[exe] {
		if r.Title != nil {
			return true
		}
	}
	for _, r := range db.globs {
		if r.Title != nil {
			if ok, _ := path.Match(r.Exe, exe); ok {
				return true
			}
		}
	}
	return false
}

// Get returns an entry by id.
func (db *DB) Get(id string) *Entry { return db.byID[id] }

// BySteamID returns the entry with the given Steam app id.
func (db *DB) BySteamID(id int) *Entry { return db.bySteam[id] }

// All returns every entry in file order.
func (db *DB) All() []*Entry { return db.entries }

// Len returns the number of entries.
func (db *DB) Len() int { return len(db.entries) }

// Count returns how many entries of a kind exist.
func (db *DB) Count(k model.Kind) int {
	n := 0
	for _, e := range db.entries {
		if e.Kind == k {
			n++
		}
	}
	return n
}
