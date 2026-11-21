package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harsiz/SimpelPlaytime/internal/model"
)

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func sess(key string, startMin, mins int) model.Session {
	st := t0.Add(time.Duration(startMin) * time.Minute)
	return model.Session{Key: key, Start: st, End: st.Add(time.Duration(mins) * time.Minute), Secs: int64(mins * 60)}
}

func TestEnsureAndReload(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	e, created, err := s.Ensure(model.Entry{Key: "db:hades-ii", Name: "Hades II", Kind: model.KindGame, ExePath: `C:\a\Hades2.exe`})
	if err != nil || !created || e.FirstSeen.IsZero() {
		t.Fatalf("Ensure: %+v %v %v", e, created, err)
	}
	// second ensure must not clobber user edits but should refresh the path
	if err := s.Update("db:hades-ii", func(e *model.Entry) { e.Name = "My Hades"; e.Favorite = true }); err != nil {
		t.Fatal(err)
	}
	e, created, _ = s.Ensure(model.Entry{Key: "db:hades-ii", Name: "Hades II", ExePath: `D:\b\Hades2.exe`})
	if created || e.Name != "My Hades" || e.ExePath != `D:\b\Hades2.exe` || !e.Favorite {
		t.Fatalf("second Ensure clobbered: %+v", e)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Entry("db:hades-ii")
	if !ok || got.Name != "My Hades" || got.Kind != model.KindGame {
		t.Fatalf("reload lost data: %+v", got)
	}
}

func TestSessionsAppendAndReload(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.Ensure(model.Entry{Key: "a", Name: "A", Kind: model.KindGame})
	for _, x := range []model.Session{sess("a", 60, 10), sess("a", 0, 30), sess("b", 30, 5)} {
		if err := s.AddSession(x); err != nil {
			t.Fatal(err)
		}
	}
	all := s.Sessions()
	if len(all) != 3 || !all[0].Start.Equal(t0) {
		t.Fatalf("sessions not sorted: %+v", all)
	}
	if got := s.SessionsFor("a"); len(got) != 2 {
		t.Errorf("SessionsFor(a) = %d", len(got))
	}
	e, _ := s.Entry("a")
	if !e.LastPlayed.Equal(t0.Add(70 * time.Minute)) {
		t.Errorf("LastPlayed = %v", e.LastPlayed)
	}
	// torn line at the end should be ignored
	f, _ := os.OpenFile(filepath.Join(dir, sessionsFile), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"id":"x","key":"a","sta`)
	f.Close()
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := s2.SessionCount(); n != 3 {
		t.Errorf("reload count = %d", n)
	}
}

func TestDeleteSessionAndEntry(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.Ensure(model.Entry{Key: "a", Name: "A"})
	s.Ensure(model.Entry{Key: "b", Name: "B"})
	s.AddSession(sess("a", 0, 10))
	s.AddSession(sess("b", 5, 10))
	s.AddSession(sess("a", 20, 10))
	first := s.SessionsFor("a")[0]
	if err := s.DeleteSession(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession("nope"); err != ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if err := s.Delete("b"); err != nil {
		t.Fatal(err)
	}
	s2, _ := Open(dir)
	if n := s2.SessionCount(); n != 1 {
		t.Errorf("after deletes count = %d", n)
	}
	if _, ok := s2.Entry("b"); ok {
		t.Error("b should be gone")
	}
}

func TestMergeCreatesAlias(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.Ensure(model.Entry{Key: "exe:osu!.exe", Name: "osu", Kind: model.KindApp, Matchers: []model.Matcher{{Type: model.MatchExe, Value: "osu!.exe"}}})
	s.Ensure(model.Entry{Key: "db:osu", Name: "osu!", Kind: model.KindGame})
	s.AddSession(sess("exe:osu!.exe", 0, 10))
	s.AddSession(sess("db:osu", 30, 10))
	if err := s.Merge("exe:osu!.exe", "db:osu"); err != nil {
		t.Fatal(err)
	}
	if s.Resolve("exe:osu!.exe") != "db:osu" {
		t.Error("alias missing")
	}
	if n := len(s.SessionsFor("db:osu")); n != 2 {
		t.Errorf("merged sessions = %d", n)
	}
	// sessions added under the old key land in the new one
	s.AddSession(sess("exe:osu!.exe", 60, 5))
	s2, _ := Open(dir)
	if n := len(s2.SessionsFor("db:osu")); n != 3 {
		t.Errorf("after reload merged sessions = %d", n)
	}
	e, ok := s2.Entry("exe:osu!.exe")
	if !ok || e.Key != "db:osu" || len(e.Matchers) != 1 {
		t.Errorf("Entry via alias = %+v", e)
	}
	if err := s2.Merge("db:osu", "db:osu"); err == nil {
		t.Error("self merge should fail")
	}
}

func TestActiveCheckpoint(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if got, err := s.LoadActive(); err != nil || got != nil {
		t.Fatalf("empty LoadActive = %v %v", got, err)
	}
	want := []model.Session{sess("a", 0, 3)}
	if err := s.SaveActive(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadActive()
	if err != nil || len(got) != 1 || got[0].Secs != 180 {
		t.Fatalf("LoadActive = %+v %v", got, err)
	}
}

func TestExportImportAndCSV(t *testing.T) {
	a, _ := Open(t.TempDir())
	a.Ensure(model.Entry{Key: "a", Name: "Game, With Comma", Kind: model.KindGame, IconFile: "x.png"})
	a.AddSession(sess("a", 0, 10))
	a.AddSession(sess("a", 20, 15))
	data := a.Export()

	b, _ := Open(t.TempDir())
	ne, ns, err := b.Import(data)
	if err != nil || ne != 1 || ns != 2 {
		t.Fatalf("Import = %d %d %v", ne, ns, err)
	}
	// importing again adds nothing
	ne, ns, _ = b.Import(data)
	if ne != 0 || ns != 0 {
		t.Errorf("second import added %d entries %d sessions", ne, ns)
	}
	e, _ := b.Entry("a")
	if e.IconFile != "" {
		t.Error("icon file should be reset on import")
	}
	var buf bytes.Buffer
	if err := b.WriteCSV(&buf); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 || !strings.Contains(lines[1], `"Game, With Comma"`) {
		t.Errorf("csv = %q", buf.String())
	}
}

func TestSubscribe(t *testing.T) {
	s, _ := Open(t.TempDir())
	var got []Change
	s.Subscribe(func(c Change) { got = append(got, c) })
	s.Ensure(model.Entry{Key: "a"})
	s.AddSession(sess("a", 0, 1))
	if len(got) != 2 || got[0].What != "entry" || got[1].What != "session" {
		t.Errorf("changes = %+v", got)
	}
}

func TestClearSessions(t *testing.T) {
	s, _ := Open(t.TempDir())
	s.Ensure(model.Entry{Key: "a"})
	s.AddSession(sess("a", 0, 1))
	s.AddSession(sess("b", 0, 1))
	if err := s.ClearSessions("a"); err != nil {
		t.Fatal(err)
	}
	if s.SessionCount() != 1 {
		t.Errorf("count = %d", s.SessionCount())
	}
	if _, ok := s.Entry("a"); !ok {
		t.Error("entry should stay")
	}
}
