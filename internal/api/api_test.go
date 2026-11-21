package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/harsiz/SimpelPlaytime/internal/core"
	"github.com/harsiz/SimpelPlaytime/internal/launchers"
	"github.com/harsiz/SimpelPlaytime/internal/model"
	"github.com/harsiz/SimpelPlaytime/internal/procs"
	"github.com/harsiz/SimpelPlaytime/internal/settings"
)

type env struct {
	c     *core.Core
	s     *Server
	h     http.Handler
	snap  procs.Snapshot
	shown int
}

func setup(t *testing.T) *env {
	t.Helper()
	e := &env{}
	c, err := core.New(core.Options{
		DataDir:   t.TempDir(),
		Version:   "test",
		Scanner:   procs.Func(func() (procs.Snapshot, error) { return e.snap, nil }),
		Launchers: launchers.NewIndex(nil, ""),
		NoArt:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
	e.s = New(c, func() { e.shown++ })
	e.h = e.s.Handler()

	now := time.Now()
	c.Store.Ensure(model.Entry{Key: "db:hades-ii", Name: "Hades II", Kind: model.KindGame, Source: model.SourceDatabase, SteamAppID: 1145350})
	c.Store.Ensure(model.Entry{Key: "db:discord", Name: "Discord", Kind: model.KindApp, Source: model.SourceDatabase})
	c.Store.Ensure(model.Entry{Key: "exe:secret.exe", Name: "Secret", Kind: model.KindApp, Hidden: true})
	add := func(key string, ago time.Duration, mins int) {
		st := now.Add(-ago)
		c.Store.AddSession(model.Session{Key: key, Start: st, End: st.Add(time.Duration(mins) * time.Minute), Secs: int64(mins * 60)})
	}
	add("db:hades-ii", 50*time.Hour, 120)
	add("db:hades-ii", 26*time.Hour, 60)
	add("db:discord", 30*time.Hour, 30)
	add("exe:secret.exe", 2*time.Hour, 10)
	return e
}

func (e *env) do(t *testing.T, method, path string, header map[string]string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Host = "127.0.0.1:47123"
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Result().Body)
	return rec.Code, body
}

func (e *env) getJSON(t *testing.T, path string, v any) {
	t.Helper()
	code, body := e.do(t, http.MethodGet, path, nil)
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, code, body)
	}
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("GET %s: bad json %v: %s", path, err, body)
	}
}

func TestIndexAndStatus(t *testing.T) {
	e := setup(t)
	var idx map[string]any
	e.getJSON(t, "/api/v1", &idx)
	if idx["app"] != "SimpelPlaytime" || len(idx["endpoints"].([]any)) < 10 {
		t.Errorf("index = %v", idx)
	}
	var st map[string]any
	e.getJSON(t, "/api/v1/status?pretty=1", &st)
	if st["entries"].(float64) != 3 || st["sessions"].(float64) != 4 || st["paused"] != false {
		t.Errorf("status = %v", st)
	}
}

func TestGamesListAndDetail(t *testing.T) {
	e := setup(t)
	var games []gameJSON
	e.getJSON(t, "/api/v1/games", &games)
	if len(games) != 2 || games[0].Key != "db:hades-ii" || games[0].TotalSecs != 10800 || games[0].Total != "3h" || games[0].TotalHours != 3 {
		t.Fatalf("games = %+v", games)
	}
	e.getJSON(t, "/api/v1/games?kind=app&hidden=1", &games)
	if len(games) != 2 {
		t.Errorf("apps incl hidden = %+v", games)
	}
	e.getJSON(t, "/api/v1/games?q=had&sort=name&limit=1", &games)
	if len(games) != 1 || games[0].Name != "Hades II" {
		t.Errorf("search = %+v", games)
	}
	var detail map[string]any
	e.getJSON(t, "/api/v1/games/"+url.PathEscape("db:hades-ii"), &detail)
	if detail["name"] != "Hades II" || detail["sessions"].(float64) != 2 || len(detail["last30Days"].([]any)) != 30 {
		t.Errorf("detail = %v", detail)
	}
	// lookup by name works too
	e.getJSON(t, "/api/v1/games/hades%20ii", &detail)
	if detail["key"] != "db:hades-ii" {
		t.Errorf("by name = %v", detail["key"])
	}
	code, _ := e.do(t, http.MethodGet, "/api/v1/games/nope", nil)
	if code != http.StatusNotFound {
		t.Errorf("missing game = %d", code)
	}
}

func TestSessionsAndDaily(t *testing.T) {
	e := setup(t)
	var sessions []sessionJSON
	e.getJSON(t, "/api/v1/games/db:hades-ii/sessions", &sessions)
	if len(sessions) != 2 || sessions[0].Secs != 3600 || sessions[0].Name != "Hades II" {
		t.Errorf("sessions = %+v", sessions)
	}
	e.getJSON(t, "/api/v1/sessions?kind=app&limit=1", &sessions)
	if len(sessions) != 1 || sessions[0].Key != "exe:secret.exe" {
		t.Errorf("app sessions = %+v", sessions)
	}
	from := time.Now().Add(-27 * time.Hour).Format(time.RFC3339)
	e.getJSON(t, "/api/v1/sessions?from="+url.QueryEscape(from), &sessions)
	if len(sessions) != 2 {
		t.Errorf("sessions since = %d", len(sessions))
	}
	code, _ := e.do(t, http.MethodGet, "/api/v1/sessions?from=yesterdayish", nil)
	if code != http.StatusBadRequest {
		t.Errorf("bad time = %d", code)
	}
	var days []map[string]any
	e.getJSON(t, "/api/v1/games/db:hades-ii/daily?days=7", &days)
	total := 0.0
	for _, d := range days {
		total += d["secs"].(float64)
	}
	if len(days) != 7 || total != 10800 {
		t.Errorf("daily = %v (total %v)", days, total)
	}
}

func TestStatsEndpoints(t *testing.T) {
	e := setup(t)
	var sum map[string]any
	e.getJSON(t, "/api/v1/stats/summary?kind=game", &sum)
	pretty := sum["pretty"].(map[string]any)
	if pretty["total"] != "3h" || pretty["mostPlayed"] != "Hades II" {
		t.Errorf("summary = %v", sum)
	}
	var chart map[string]any
	e.getJSON(t, "/api/v1/stats/chart?range=7d&breakdown=1", &chart)
	if chart["bucket"] != "day" || len(chart["buckets"].([]any)) != 7 {
		t.Errorf("chart = %v", chart)
	}
	var top []map[string]any
	e.getJSON(t, "/api/v1/stats/top?limit=1", &top)
	if len(top) != 1 || top[0]["name"] != "Hades II" || top[0]["rank"].(float64) != 1 {
		t.Errorf("top = %v", top)
	}
	var hm map[string]any
	e.getJSON(t, "/api/v1/stats/heatmap", &hm)
	if len(hm["hoursOfDay"].(map[string]any)) != 7 {
		t.Errorf("heatmap = %v", hm)
	}
}

func TestNowPlaying(t *testing.T) {
	e := setup(t)
	e.snap = procs.Snapshot{Procs: []procs.Process{{PID: 9, Name: "Hades2.exe", Path: `C:\g\Hades2.exe`}}}
	e.c.Tracker.Tick()
	e.c.Tracker.Tick()
	var live []liveJSON
	e.getJSON(t, "/api/v1/now-playing", &live)
	if len(live) != 1 || live[0].Key != "db:hades-ii" || !live[0].Running || live[0].TotalSecs < 10800 || live[0].SteamAppID != 1145350 {
		t.Errorf("now playing = %+v", live)
	}
}

func TestExports(t *testing.T) {
	e := setup(t)
	var backup map[string]any
	e.getJSON(t, "/api/v1/export", &backup)
	if len(backup["sessions"].([]any)) != 4 {
		t.Errorf("export = %v", backup)
	}
	code, body := e.do(t, http.MethodGet, "/api/v1/export.csv", nil)
	if code != 200 || strings.Count(string(body), "\n") != 5 {
		t.Errorf("csv = %d %q", code, body)
	}
}

func TestControlEndpoints(t *testing.T) {
	e := setup(t)
	if code, _ := e.do(t, http.MethodPost, "/api/v1/tracking/pause", nil); code != 200 || !e.c.Tracker.Paused() {
		t.Error("pause failed")
	}
	if code, _ := e.do(t, http.MethodPost, "/api/v1/tracking/resume", nil); code != 200 || e.c.Tracker.Paused() {
		t.Error("resume failed")
	}
	e.do(t, http.MethodPost, "/api/v1/app/show", nil)
	if e.shown != 1 {
		t.Error("show not called")
	}
	if code, _ := e.do(t, http.MethodGet, "/api/v1/tracking/pause", nil); code != http.StatusMethodNotAllowed {
		t.Errorf("GET on POST route = %d", code)
	}
}

func TestSecurity(t *testing.T) {
	e := setup(t)
	// DNS rebinding: evil.com resolving to 127.0.0.1
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Host = "evil.example.com:47123"
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("foreign host = %d", rec.Code)
	}
	// browsers cannot POST without CORS enabled
	if code, _ := e.do(t, http.MethodPost, "/api/v1/tracking/pause", map[string]string{"Origin": "https://evil.example.com"}); code != http.StatusForbidden {
		t.Errorf("cross origin post = %d", code)
	}
	// no CORS header by default
	req = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Host = "localhost:47123"
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS should be off by default")
	}

	e.c.Settings.Update(func(s *settings.Settings) { s.APIRequireToken = true; s.APIAllowCORS = true; s.APIToken = "tok123" })
	if code, _ := e.do(t, http.MethodGet, "/api/v1/status", nil); code != http.StatusUnauthorized {
		t.Errorf("no token = %d", code)
	}
	if code, _ := e.do(t, http.MethodGet, "/api/v1/status?token=tok123", nil); code != 200 {
		t.Errorf("query token = %d", code)
	}
	code, _ := e.do(t, http.MethodGet, "/api/v1/status", map[string]string{"Authorization": "Bearer tok123"})
	if code != 200 {
		t.Errorf("bearer token = %d", code)
	}
	if code, _ := e.do(t, http.MethodGet, "/api/v1/status", map[string]string{"Authorization": "Bearer wrong"}); code != http.StatusUnauthorized {
		t.Errorf("wrong token = %d", code)
	}
	req = httptest.NewRequest(http.MethodOptions, "/api/v1/status", nil)
	req.Host = "127.0.0.1"
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("preflight = %d %v", rec.Code, rec.Header())
	}
}

func TestApplyListens(t *testing.T) {
	e := setup(t)
	e.c.Settings.Update(func(s *settings.Settings) { s.APIPort = 47999; s.APIEnabled = true })
	if err := e.s.Apply(); err != nil {
		t.Skipf("port busy: %v", err)
	}
	defer e.s.Stop()
	resp, err := http.Get("http://" + e.s.Addr() + "/api/v1/ping")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("ping = %d", resp.StatusCode)
	}
	e.c.Settings.Update(func(s *settings.Settings) { s.APIEnabled = false })
	e.s.Apply()
	if e.s.Addr() != "" {
		t.Error("server should be stopped")
	}
}
