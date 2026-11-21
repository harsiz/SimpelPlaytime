// Package store keeps the library (games + apps) and all play sessions on
// disk. It is deliberately simple:
//
//	library.json    every entry plus merge aliases (rewritten atomically)
//	sessions.jsonl  one finished session per line (append only)
//	active.json     sessions that are still running (checkpointed)
//
// Everything is also kept in memory, so reads are cheap.
package store

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/harsiz/SimpelPlaytime/internal/fsutil"
	"github.com/harsiz/SimpelPlaytime/internal/model"
)

const (
	libraryFile  = "library.json"
	sessionsFile = "sessions.jsonl"
	activeFile   = "active.json"
)

// ErrNotFound is returned when an entry or session does not exist.
var ErrNotFound = errors.New("not found")

// Change describes what changed, for listeners.
type Change struct {
	What string // "entry", "session", "all"
	Key  string
}

type libraryDoc struct {
	Version int               `json:"version"`
	Entries []*model.Entry    `json:"entries"`
	Aliases map[string]string `json:"aliases,omitempty"`
}

// Store is the on disk database.
type Store struct {
	dir string

	mu       sync.RWMutex
	entries  map[string]*model.Entry
	aliases  map[string]string
	sessions []model.Session

	lmu       sync.Mutex
	listeners []func(Change)
}

// Open loads (or creates) the store in dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	for _, sub := range []string{"icons", "art"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	s := &Store{dir: dir, entries: map[string]*model.Entry{}, aliases: map[string]string{}}
	if err := s.loadLibrary(); err != nil {
		return nil, fmt.Errorf("load library: %w", err)
	}
	if err := s.loadSessions(); err != nil {
		return nil, fmt.Errorf("load sessions: %w", err)
	}
	return s, nil
}

// Dir is the data directory.
func (s *Store) Dir() string { return s.dir }

// IconsDir is where extracted exe icons are cached.
func (s *Store) IconsDir() string { return filepath.Join(s.dir, "icons") }

// ArtDir is where posters and hero images are cached.
func (s *Store) ArtDir() string { return filepath.Join(s.dir, "art") }

// IconPath returns the full path of an entry icon, or "" if it has none.
func (s *Store) IconPath(e model.Entry) string {
	if e.IconFile == "" {
		return ""
	}
	return filepath.Join(s.IconsDir(), e.IconFile)
}

// PosterPath returns the full path of an entry poster, or "".
func (s *Store) PosterPath(e model.Entry) string {
	if e.PosterFile == "" {
		return ""
	}
	return filepath.Join(s.ArtDir(), e.PosterFile)
}

// HeroPath returns the full path of an entry hero background, or "".
func (s *Store) HeroPath(e model.Entry) string {
	if e.HeroFile == "" {
		return ""
	}
	return filepath.Join(s.ArtDir(), e.HeroFile)
}

func (s *Store) loadLibrary() error {
	data, err := os.ReadFile(filepath.Join(s.dir, libraryFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var doc libraryDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		_ = os.WriteFile(filepath.Join(s.dir, libraryFile+".broken"), data, 0o600)
		return nil
	}
	for _, e := range doc.Entries {
		if e == nil || e.Key == "" {
			continue
		}
		if !e.Kind.Valid() {
			e.Kind = model.KindApp
		}
		s.entries[e.Key] = e
	}
	for k, v := range doc.Aliases {
		s.aliases[k] = v
	}
	return nil
}

func (s *Store) loadSessions() error {
	f, err := os.Open(filepath.Join(s.dir, sessionsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var sess model.Session
		if err := json.Unmarshal(line, &sess); err != nil {
			// A torn last line after a crash is skipped, not fatal.
			continue
		}
		if sess.Key == "" || sess.Secs < 0 {
			continue
		}
		sess.Key = s.resolveLocked(sess.Key)
		s.sessions = append(s.sessions, sess)
	}
	sort.SliceStable(s.sessions, func(i, j int) bool { return s.sessions[i].Start.Before(s.sessions[j].Start) })
	return sc.Err()
}

func (s *Store) saveLibraryLocked() error {
	doc := libraryDoc{Version: 1, Aliases: s.aliases}
	keys := make([]string, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		doc.Entries = append(doc.Entries, s.entries[k])
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(s.dir, libraryFile), data, 0o600)
}

func (s *Store) rewriteSessionsLocked() error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, sess := range s.sessions {
		if err := enc.Encode(sess); err != nil {
			return err
		}
	}
	return fsutil.WriteFileAtomic(filepath.Join(s.dir, sessionsFile), buf.Bytes(), 0o600)
}

// Subscribe registers fn to be called (from the writer goroutine) after every
// change.
func (s *Store) Subscribe(fn func(Change)) {
	s.lmu.Lock()
	s.listeners = append(s.listeners, fn)
	s.lmu.Unlock()
}

func (s *Store) notify(c Change) {
	s.lmu.Lock()
	ls := append([]func(Change){}, s.listeners...)
	s.lmu.Unlock()
	for _, l := range ls {
		l(c)
	}
}

func (s *Store) resolveLocked(key string) string {
	for i := 0; i < 16; i++ {
		next, ok := s.aliases[key]
		if !ok {
			return key
		}
		key = next
	}
	return key
}

// Resolve follows merge aliases to the real entry key.
func (s *Store) Resolve(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.resolveLocked(key)
}

func copyEntry(e *model.Entry) model.Entry {
	c := *e
	c.Matchers = append([]model.Matcher(nil), e.Matchers...)
	c.Tags = append([]string(nil), e.Tags...)
	if e.Meta != nil {
		m := *e.Meta
		m.Genres = append([]string(nil), e.Meta.Genres...)
		m.Developers = append([]string(nil), e.Meta.Developers...)
		m.Publishers = append([]string(nil), e.Meta.Publishers...)
		c.Meta = &m
	}
	return c
}

// Entries returns copies of all entries sorted by name.
func (s *Store) Entries() []model.Entry {
	s.mu.RLock()
	out := make([]model.Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, copyEntry(e))
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].DisplayName()) < strings.ToLower(out[j].DisplayName())
	})
	return out
}

// Entry returns one entry (following aliases).
func (s *Store) Entry(key string) (model.Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[s.resolveLocked(key)]
	if !ok {
		return model.Entry{}, false
	}
	return copyEntry(e), true
}

// Ensure creates the entry if it does not exist yet. When it exists, only the
// exe path/name are refreshed (so user edits survive). It reports whether the
// entry was created.
func (s *Store) Ensure(e model.Entry) (model.Entry, bool, error) {
	s.mu.Lock()
	key := s.resolveLocked(e.Key)
	if cur, ok := s.entries[key]; ok {
		changed := false
		if e.ExePath != "" && cur.ExePath != e.ExePath {
			cur.ExePath = e.ExePath
			changed = true
		}
		if e.ExeName != "" && cur.ExeName == "" {
			cur.ExeName = e.ExeName
			changed = true
		}
		if cur.SteamAppID == 0 && e.SteamAppID != 0 {
			cur.SteamAppID = e.SteamAppID
			changed = true
		}
		var err error
		if changed {
			err = s.saveLibraryLocked()
		}
		out := copyEntry(cur)
		s.mu.Unlock()
		if changed {
			s.notify(Change{What: "entry", Key: key})
		}
		return out, false, err
	}
	if e.Key == "" {
		s.mu.Unlock()
		return model.Entry{}, false, errors.New("entry without key")
	}
	if !e.Kind.Valid() {
		e.Kind = model.KindApp
	}
	if e.FirstSeen.IsZero() {
		e.FirstSeen = time.Now()
	}
	ne := e
	s.entries[e.Key] = &ne
	err := s.saveLibraryLocked()
	out := copyEntry(&ne)
	s.mu.Unlock()
	s.notify(Change{What: "entry", Key: e.Key})
	return out, true, err
}

// Put inserts or replaces an entry.
func (s *Store) Put(e model.Entry) error {
	if e.Key == "" {
		return errors.New("entry without key")
	}
	if !e.Kind.Valid() {
		e.Kind = model.KindApp
	}
	s.mu.Lock()
	ne := copyEntry(&e)
	s.entries[e.Key] = &ne
	delete(s.aliases, e.Key)
	err := s.saveLibraryLocked()
	s.mu.Unlock()
	s.notify(Change{What: "entry", Key: e.Key})
	return err
}

// Update edits an entry in place.
func (s *Store) Update(key string, fn func(*model.Entry)) error {
	s.mu.Lock()
	key = s.resolveLocked(key)
	e, ok := s.entries[key]
	if !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	fn(e)
	e.Key = key // the key is the identity, never let fn change it
	if !e.Kind.Valid() {
		e.Kind = model.KindApp
	}
	err := s.saveLibraryLocked()
	s.mu.Unlock()
	s.notify(Change{What: "entry", Key: key})
	return err
}

// Delete removes an entry and all of its sessions.
func (s *Store) Delete(key string) error {
	s.mu.Lock()
	key = s.resolveLocked(key)
	if _, ok := s.entries[key]; !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	delete(s.entries, key)
	for k, v := range s.aliases {
		if v == key {
			delete(s.aliases, k)
		}
	}
	kept := s.sessions[:0]
	for _, sess := range s.sessions {
		if sess.Key != key {
			kept = append(kept, sess)
		}
	}
	s.sessions = kept
	err := s.saveLibraryLocked()
	if err2 := s.rewriteSessionsLocked(); err == nil {
		err = err2
	}
	s.mu.Unlock()
	s.notify(Change{What: "all", Key: key})
	return err
}

// Merge moves every session of from into into, removes from and remembers an
// alias so future detections of from land in into.
func (s *Store) Merge(from, into string) error {
	s.mu.Lock()
	from, into = s.resolveLocked(from), s.resolveLocked(into)
	if from == into {
		s.mu.Unlock()
		return errors.New("cannot merge an entry into itself")
	}
	fe, ok1 := s.entries[from]
	te, ok2 := s.entries[into]
	if !ok1 || !ok2 {
		s.mu.Unlock()
		return ErrNotFound
	}
	for i := range s.sessions {
		if s.sessions[i].Key == from {
			s.sessions[i].Key = into
		}
	}
	te.Matchers = append(te.Matchers, fe.Matchers...)
	if fe.LastPlayed.After(te.LastPlayed) {
		te.LastPlayed = fe.LastPlayed
	}
	if !fe.FirstSeen.IsZero() && (te.FirstSeen.IsZero() || fe.FirstSeen.Before(te.FirstSeen)) {
		te.FirstSeen = fe.FirstSeen
	}
	if te.IconFile == "" {
		te.IconFile = fe.IconFile
	}
	if te.ExePath == "" {
		te.ExePath = fe.ExePath
	}
	delete(s.entries, from)
	s.aliases[from] = into
	for k, v := range s.aliases {
		if v == from {
			s.aliases[k] = into
		}
	}
	err := s.saveLibraryLocked()
	if err2 := s.rewriteSessionsLocked(); err == nil {
		err = err2
	}
	s.mu.Unlock()
	s.notify(Change{What: "all", Key: into})
	return err
}

// NewID makes a sortable unique id.
func NewID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return strconv.FormatInt(time.Now().UnixNano(), 36) + hex.EncodeToString(b)
}

// AddSession appends a finished session.
func (s *Store) AddSession(sess model.Session) error {
	if sess.Key == "" {
		return errors.New("session without key")
	}
	if sess.ID == "" {
		sess.ID = NewID()
	}
	if sess.End.Before(sess.Start) {
		sess.End = sess.Start
	}
	if sess.Secs < 0 {
		sess.Secs = 0
	}
	if sess.Focus > sess.Secs {
		sess.Focus = sess.Secs
	}
	s.mu.Lock()
	sess.Key = s.resolveLocked(sess.Key)
	line, err := json.Marshal(sess)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, sessionsFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, err = f.Write(append(line, '\n'))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	// insert keeping start order (new sessions are almost always last)
	i := sort.Search(len(s.sessions), func(i int) bool { return s.sessions[i].Start.After(sess.Start) })
	s.sessions = append(s.sessions, model.Session{})
	copy(s.sessions[i+1:], s.sessions[i:])
	s.sessions[i] = sess
	if e, ok := s.entries[sess.Key]; ok && sess.End.After(e.LastPlayed) {
		e.LastPlayed = sess.End
		if err2 := s.saveLibraryLocked(); err == nil {
			err = err2
		}
	}
	s.mu.Unlock()
	s.notify(Change{What: "session", Key: sess.Key})
	return err
}

// DeleteSession removes one session by id.
func (s *Store) DeleteSession(id string) error {
	s.mu.Lock()
	idx := -1
	for i, sess := range s.sessions {
		if sess.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return ErrNotFound
	}
	key := s.sessions[idx].Key
	s.sessions = append(s.sessions[:idx], s.sessions[idx+1:]...)
	err := s.rewriteSessionsLocked()
	s.mu.Unlock()
	s.notify(Change{What: "session", Key: key})
	return err
}

// ClearSessions deletes every session of one entry (keeps the entry).
func (s *Store) ClearSessions(key string) error {
	s.mu.Lock()
	key = s.resolveLocked(key)
	kept := s.sessions[:0]
	for _, sess := range s.sessions {
		if sess.Key != key {
			kept = append(kept, sess)
		}
	}
	s.sessions = kept
	err := s.rewriteSessionsLocked()
	s.mu.Unlock()
	s.notify(Change{What: "session", Key: key})
	return err
}

// Sessions returns a copy of every finished session, oldest first.
func (s *Store) Sessions() []model.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]model.Session(nil), s.sessions...)
}

// SessionsFor returns the sessions of one entry, oldest first.
func (s *Store) SessionsFor(key string) []model.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key = s.resolveLocked(key)
	var out []model.Session
	for _, sess := range s.sessions {
		if sess.Key == key {
			out = append(out, sess)
		}
	}
	return out
}

// SessionCount returns how many finished sessions are stored.
func (s *Store) SessionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// SaveActive checkpoints the running sessions so a crash loses almost nothing.
func (s *Store) SaveActive(active []model.Session) error {
	data, err := json.Marshal(active)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(s.dir, activeFile), data, 0o600)
}

// LoadActive returns the last checkpoint of running sessions.
func (s *Store) LoadActive() ([]model.Session, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, activeFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []model.Session
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, nil
	}
	s.mu.RLock()
	for i := range out {
		out[i].Key = s.resolveLocked(out[i].Key)
	}
	s.mu.RUnlock()
	return out, nil
}

// ExportData is a full backup.
type ExportData struct {
	App        string            `json:"app"`
	Version    int               `json:"version"`
	ExportedAt time.Time         `json:"exportedAt"`
	Entries    []model.Entry     `json:"entries"`
	Aliases    map[string]string `json:"aliases,omitempty"`
	Sessions   []model.Session   `json:"sessions"`
}

// Export returns a full backup of the library and sessions.
func (s *Store) Export() ExportData {
	s.mu.RLock()
	aliases := map[string]string{}
	for k, v := range s.aliases {
		aliases[k] = v
	}
	s.mu.RUnlock()
	return ExportData{
		App:        "SimpelPlaytime",
		Version:    1,
		ExportedAt: time.Now(),
		Entries:    s.Entries(),
		Aliases:    aliases,
		Sessions:   s.Sessions(),
	}
}

// Import merges a backup in. Existing entries are kept (only missing ones are
// added) and sessions are de-duplicated by id. It returns how many entries and
// sessions were added.
func (s *Store) Import(d ExportData) (entries, sessions int, err error) {
	s.mu.Lock()
	for k, v := range d.Aliases {
		if _, exists := s.entries[k]; !exists {
			if _, has := s.aliases[k]; !has {
				s.aliases[k] = v
			}
		}
	}
	for _, e := range d.Entries {
		if e.Key == "" {
			continue
		}
		key := s.resolveLocked(e.Key)
		if _, ok := s.entries[key]; ok {
			continue
		}
		ne := copyEntry(&e)
		ne.Key = key
		if !ne.Kind.Valid() {
			ne.Kind = model.KindApp
		}
		// art files are not part of the backup, fetch them again
		ne.IconFile, ne.PosterFile, ne.HeroFile = "", "", ""
		ne.ArtChecked, ne.IconChecked = time.Time{}, time.Time{}
		s.entries[key] = &ne
		entries++
	}
	ids := map[string]bool{}
	for _, sess := range s.sessions {
		ids[sess.ID] = true
	}
	for _, sess := range d.Sessions {
		if sess.Key == "" || (sess.ID != "" && ids[sess.ID]) {
			continue
		}
		if sess.ID == "" {
			sess.ID = NewID()
		}
		sess.Key = s.resolveLocked(sess.Key)
		ids[sess.ID] = true
		s.sessions = append(s.sessions, sess)
		sessions++
	}
	sort.SliceStable(s.sessions, func(i, j int) bool { return s.sessions[i].Start.Before(s.sessions[j].Start) })
	err = s.saveLibraryLocked()
	if err2 := s.rewriteSessionsLocked(); err == nil {
		err = err2
	}
	s.mu.Unlock()
	s.notify(Change{What: "all"})
	return entries, sessions, err
}

// WriteCSV writes every session as CSV (handy for spreadsheets).
func (s *Store) WriteCSV(w io.Writer) error {
	names := map[string]model.Entry{}
	for _, e := range s.Entries() {
		names[e.Key] = e
	}
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"id", "key", "name", "kind", "start", "end", "seconds", "focus_seconds", "manual"}); err != nil {
		return err
	}
	for _, sess := range s.Sessions() {
		e := names[sess.Key]
		rec := []string{
			sess.ID, sess.Key, e.DisplayName(), string(e.Kind),
			sess.Start.Format(time.RFC3339), sess.End.Format(time.RFC3339),
			strconv.FormatInt(sess.Secs, 10), strconv.FormatInt(sess.Focus, 10),
			strconv.FormatBool(sess.Manual),
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
