// Package api is the local JSON API (http://127.0.0.1:47123/api/v1 by
// default). It only listens on loopback, checks the Host header against DNS
// rebinding, and can require a token.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/harsiz/SimpelPlaytime/internal/core"
	"github.com/harsiz/SimpelPlaytime/internal/model"
	"github.com/harsiz/SimpelPlaytime/internal/settings"
	"github.com/harsiz/SimpelPlaytime/internal/stats"
	"github.com/harsiz/SimpelPlaytime/internal/tracker"
)

// Server is the API server.
type Server struct {
	core   *core.Core
	onShow func()

	mu      sync.Mutex
	srv     *http.Server
	addr    string
	lastErr error
	port    int
}

// New makes a server. onShow is called by POST /api/v1/app/show.
func New(c *core.Core, onShow func()) *Server {
	return &Server{core: c, onShow: onShow}
}

// Addr returns the listen address ("" when stopped).
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// LastError returns why the server is not running (if it should be).
func (s *Server) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// Apply starts, stops or restarts the server to match the settings.
func (s *Server) Apply() error {
	st := s.core.Settings.Get()
	s.mu.Lock()
	running := s.srv != nil
	samePort := s.port == st.APIPort
	s.mu.Unlock()
	switch {
	case !st.APIEnabled:
		s.Stop()
		return nil
	case running && samePort:
		return nil
	case running:
		s.Stop()
	}
	return s.start(st.APIPort)
}

func (s *Server) start(port int) error {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		s.mu.Lock()
		s.lastErr = fmt.Errorf("could not listen on port %d: %w", port, err)
		s.mu.Unlock()
		return err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	s.mu.Lock()
	s.srv, s.port, s.addr, s.lastErr = srv, port, ln.Addr().String(), nil
	s.mu.Unlock()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.mu.Lock()
			s.lastErr = err
			s.mu.Unlock()
		}
	}()
	return nil
}

// Stop shuts the server down.
func (s *Server) Stop() {
	s.mu.Lock()
	srv := s.srv
	s.srv, s.addr, s.port = nil, "", 0
	s.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}
}

// Handler returns the API routes (also used directly by tests).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1", s.index)
	mux.HandleFunc("GET /api/v1/{$}", s.index)
	mux.HandleFunc("GET /api/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, r, map[string]any{"app": "SimpelPlaytime", "ok": true})
	})
	mux.HandleFunc("GET /api/v1/status", s.status)
	mux.HandleFunc("GET /api/v1/now-playing", s.nowPlaying)
	mux.HandleFunc("GET /api/v1/games", s.games)
	mux.HandleFunc("GET /api/v1/games/{key}", s.game)
	mux.HandleFunc("GET /api/v1/games/{key}/sessions", s.gameSessions)
	mux.HandleFunc("GET /api/v1/games/{key}/daily", s.gameDaily)
	mux.HandleFunc("GET /api/v1/sessions", s.sessions)
	mux.HandleFunc("GET /api/v1/stats/summary", s.summary)
	mux.HandleFunc("GET /api/v1/stats/chart", s.chart)
	mux.HandleFunc("GET /api/v1/stats/top", s.top)
	mux.HandleFunc("GET /api/v1/stats/heatmap", s.heatmap)
	mux.HandleFunc("GET /api/v1/export", s.export)
	mux.HandleFunc("GET /api/v1/export.csv", s.exportCSV)
	mux.HandleFunc("POST /api/v1/tracking/pause", func(w http.ResponseWriter, r *http.Request) {
		s.core.Tracker.SetPaused(true)
		writeJSON(w, r, map[string]any{"paused": true})
	})
	mux.HandleFunc("POST /api/v1/tracking/resume", func(w http.ResponseWriter, r *http.Request) {
		s.core.Tracker.SetPaused(false)
		writeJSON(w, r, map[string]any{"paused": false})
	})
	mux.HandleFunc("POST /api/v1/app/show", func(w http.ResponseWriter, r *http.Request) {
		if s.onShow != nil {
			s.onShow()
		}
		writeJSON(w, r, map[string]any{"shown": true})
	})
	return s.guard(mux)
}

// guard applies host checks, CORS and the optional token.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := s.core.Settings.Get()
		if !allowedHost(r.Host) {
			writeErr(w, r, http.StatusForbidden, "requests must be sent to localhost")
			return
		}
		if st.APIAllowCORS {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		} else if origin := r.Header.Get("Origin"); origin != "" && r.Method != http.MethodGet {
			// a web page trying to POST to us without CORS being allowed
			writeErr(w, r, http.StatusForbidden, "browser requests are disabled (enable CORS in settings)")
			return
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if st.APIRequireToken && !tokenOK(r, st) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="SimpelPlaytime"`)
			writeErr(w, r, http.StatusUnauthorized, "missing or wrong token (send 'Authorization: Bearer <token>' or ?token=)")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func tokenOK(r *http.Request, st settings.Settings) bool {
	got := r.URL.Query().Get("token")
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got = strings.TrimPrefix(h, "Bearer ")
	}
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(st.APIToken)) == 1
}

func allowedHost(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.Trim(strings.ToLower(h), "[]")
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || h == ""
}

func writeJSON(w http.ResponseWriter, r *http.Request, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	if r.URL.Query().Get("pretty") != "" {
		enc.SetIndent("", "  ")
	}
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, r *http.Request, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg, "status": code})
}

func hours(secs float64) float64 { return math.Round(secs/3600*100) / 100 }

// ---- payloads -------------------------------------------------------------

type gameJSON struct {
	Key            string      `json:"key"`
	Name           string      `json:"name"`
	Kind           model.Kind  `json:"kind"`
	Source         string      `json:"source"`
	SteamAppID     int         `json:"steamAppId,omitempty"`
	Exe            string      `json:"exe,omitempty"`
	ExePath        string      `json:"exePath,omitempty"`
	Favorite       bool        `json:"favorite"`
	Hidden         bool        `json:"hidden"`
	Playing        bool        `json:"playing"`
	TotalSecs      int64       `json:"totalSecs"`
	TotalHours     float64     `json:"totalHours"`
	Total          string      `json:"total"`
	Sessions       int         `json:"sessions"`
	AvgSessionSecs int64       `json:"avgSessionSecs"`
	LongestSecs    int64       `json:"longestSessionSecs"`
	FocusRatio     float64     `json:"focusRatio"`
	FirstPlayed    *time.Time  `json:"firstPlayed,omitempty"`
	LastPlayed     *time.Time  `json:"lastPlayed,omitempty"`
	FirstSeen      time.Time   `json:"firstSeen"`
	Meta           *model.Meta `json:"meta,omitempty"`
	Notes          string      `json:"notes,omitempty"`
}

func tptr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (s *Server) gameOf(e model.Entry, t stats.Totals, playing bool, full bool) gameJSON {
	g := gameJSON{
		Key: e.Key, Name: e.DisplayName(), Kind: e.Kind, Source: string(e.Source), SteamAppID: e.SteamAppID,
		Exe: e.ExeName, Favorite: e.Favorite, Hidden: e.Hidden, Playing: playing,
		TotalSecs: t.Secs, TotalHours: hours(float64(t.Secs)), Total: stats.FormatHM(float64(t.Secs)),
		Sessions: t.Sessions, AvgSessionSecs: int64(t.Average()), LongestSecs: t.Longest,
		FocusRatio: math.Round(t.FocusRatio()*1000) / 1000, FirstPlayed: tptr(t.First), FirstSeen: e.FirstSeen,
		Meta: e.Meta,
	}
	last := t.Last
	if e.LastPlayed.After(last) {
		last = e.LastPlayed
	}
	g.LastPlayed = tptr(last)
	if full {
		g.ExePath, g.Notes = e.ExePath, e.Notes
	}
	return g
}

type liveJSON struct {
	Key         string     `json:"key"`
	Name        string     `json:"name"`
	Kind        model.Kind `json:"kind"`
	Start       time.Time  `json:"start"`
	SessionSecs int64      `json:"sessionSecs"`
	Session     string     `json:"session"`
	FocusSecs   int64      `json:"focusSecs"`
	TotalSecs   int64      `json:"totalSecs"`
	Total       string     `json:"total"`
	Running     bool       `json:"running"`
	Focused     bool       `json:"focused"`
	Idle        bool       `json:"idle"`
	SteamAppID  int        `json:"steamAppId,omitempty"`
	PIDs        []uint32   `json:"pids,omitempty"`
}

func (s *Server) liveOf(l tracker.Live, totals map[string]stats.Totals) liveJSON {
	j := liveJSON{
		Key: l.Key, Name: l.Name, Kind: l.Kind, Start: l.Start, SessionSecs: l.Secs, Session: stats.FormatClock(l.Secs),
		FocusSecs: l.Focus, Running: l.Running, Focused: l.Focused, Idle: l.Idle, PIDs: l.PIDs,
		TotalSecs: totals[l.Key].Secs, Total: stats.FormatHM(float64(totals[l.Key].Secs)),
	}
	if e, ok := s.core.Store.Entry(l.Key); ok {
		j.Name, j.SteamAppID = e.DisplayName(), e.SteamAppID
	}
	return j
}

// ---- handlers -------------------------------------------------------------

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, map[string]any{
		"app":     "SimpelPlaytime",
		"version": s.core.Version,
		"tip":     "add ?pretty=1 to any endpoint for readable JSON",
		"endpoints": []string{
			"GET  /api/v1/status",
			"GET  /api/v1/now-playing",
			"GET  /api/v1/games?kind=game|app|all&sort=playtime|recent|name&q=&limit=&hidden=1",
			"GET  /api/v1/games/{key}",
			"GET  /api/v1/games/{key}/sessions?limit=",
			"GET  /api/v1/games/{key}/daily?days=30",
			"GET  /api/v1/sessions?from=&to=&key=&kind=&limit=",
			"GET  /api/v1/stats/summary?kind=",
			"GET  /api/v1/stats/chart?range=7d|30d|90d|year|12m|all&bucket=day|week|month&kind=",
			"GET  /api/v1/stats/top?range=&limit=&kind=",
			"GET  /api/v1/stats/heatmap?kind=",
			"GET  /api/v1/export",
			"GET  /api/v1/export.csv",
			"POST /api/v1/tracking/pause",
			"POST /api/v1/tracking/resume",
			"POST /api/v1/app/show",
		},
	})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	live := s.core.Tracker.Live()
	running := 0
	for _, l := range live {
		if l.Running {
			running++
		}
	}
	var scanErr string
	if err := s.core.Tracker.LastError(); err != nil {
		scanErr = err.Error()
	}
	writeJSON(w, r, map[string]any{
		"app":        "SimpelPlaytime",
		"version":    s.core.Version,
		"now":        now,
		"startedAt":  s.core.StartedAt,
		"uptimeSecs": int64(now.Sub(s.core.StartedAt).Seconds()),
		"paused":     s.core.Tracker.Paused(),
		"running":    running,
		"entries":    len(s.core.Store.Entries()),
		"sessions":   s.core.Store.SessionCount(),
		"trackMode":  s.core.Settings.Get().TrackMode,
		"scanError":  scanErr,
	})
}

func (s *Server) nowPlaying(w http.ResponseWriter, r *http.Request) {
	totals := s.core.Totals()
	out := []liveJSON{}
	all := r.URL.Query().Get("all") != ""
	for _, l := range s.core.Tracker.Live() {
		if l.Running || all {
			out = append(out, s.liveOf(l, totals))
		}
	}
	writeJSON(w, r, out)
}

func intParam(r *http.Request, name string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
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

func (s *Server) games(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind == "" {
		kind = "all"
	}
	search := strings.ToLower(q.Get("q"))
	showHidden := q.Get("hidden") != ""
	totals := s.core.Totals()
	playing := map[string]bool{}
	for _, l := range s.core.Tracker.Live() {
		if l.Running {
			playing[l.Key] = true
		}
	}
	out := []gameJSON{}
	for _, e := range s.core.Store.Entries() {
		if kind != "all" && string(e.Kind) != kind {
			continue
		}
		if e.Hidden && !showHidden {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(e.DisplayName()), search) && !strings.Contains(strings.ToLower(e.Key), search) {
			continue
		}
		out = append(out, s.gameOf(e, totals[e.Key], playing[e.Key], false))
	}
	switch q.Get("sort") {
	case "name":
		sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	case "recent":
		sort.SliceStable(out, func(i, j int) bool {
			a, b := out[i].LastPlayed, out[j].LastPlayed
			if a == nil || b == nil {
				return a != nil
			}
			return a.After(*b)
		})
	default:
		sort.SliceStable(out, func(i, j int) bool { return out[i].TotalSecs > out[j].TotalSecs })
	}
	if n := intParam(r, "limit", 0, 0, 100000); n > 0 && len(out) > n {
		out = out[:n]
	}
	writeJSON(w, r, out)
}

func (s *Server) lookup(w http.ResponseWriter, r *http.Request) (model.Entry, bool) {
	key := r.PathValue("key")
	e, ok := s.core.Store.Entry(key)
	if !ok {
		// be nice to scripts: allow a case insensitive name too
		for _, x := range s.core.Store.Entries() {
			if strings.EqualFold(x.DisplayName(), key) {
				return x, true
			}
		}
		writeErr(w, r, http.StatusNotFound, "no game or app with key "+strconv.Quote(key))
		return model.Entry{}, false
	}
	return e, true
}

func (s *Server) game(w http.ResponseWriter, r *http.Request) {
	e, ok := s.lookup(w, r)
	if !ok {
		return
	}
	totals := s.core.Totals()
	live, playing := s.core.Tracker.LiveFor(e.Key)
	out := struct {
		gameJSON
		Live     *liveJSON         `json:"live,omitempty"`
		Matchers []model.Matcher   `json:"matchers,omitempty"`
		Recent   []stats.Bucket    `json:"last30Days"`
		Summary  stats.Summary     `json:"summary"`
		Periods  map[string]string `json:"periods"`
	}{gameJSON: s.gameOf(e, totals[e.Key], playing && live.Running, true), Matchers: e.Matchers}
	if playing {
		lj := s.liveOf(live, totals)
		out.Live = &lj
	}
	sessions := s.core.Sessions(true)
	only := func(k string) bool { return k == e.Key }
	now := time.Now()
	from, to, _ := stats.Resolve("30d", now, time.Time{})
	out.Recent = stripKeys(stats.Buckets(sessions, from, to, stats.Day, s.weekStart(), only))
	out.Summary = stats.Summarize(sessions, only, now, s.weekStart())
	out.Periods = map[string]string{
		"today": stats.FormatHM(out.Summary.Today), "week": stats.FormatHM(out.Summary.Week),
		"month": stats.FormatHM(out.Summary.Month), "year": stats.FormatHM(out.Summary.Year),
	}
	writeJSON(w, r, out)
}

func stripKeys(b []stats.Bucket) []stats.Bucket {
	for i := range b {
		b[i].ByKey = nil
		b[i].Secs = math.Round(b[i].Secs)
	}
	return b
}

func (s *Server) weekStart() time.Weekday {
	if s.core.Settings.Get().WeekStartsMonday {
		return time.Monday
	}
	return time.Sunday
}

type sessionJSON struct {
	model.Session
	Name     string `json:"name"`
	Duration string `json:"duration"`
	Live     bool   `json:"live,omitempty"`
}

func (s *Server) sessionList(r *http.Request, sessions []model.Session, names map[string]string) []sessionJSON {
	limit := intParam(r, "limit", 0, 0, 1000000)
	out := make([]sessionJSON, 0, len(sessions))
	// newest first
	for i := len(sessions) - 1; i >= 0; i-- {
		ss := sessions[i]
		out = append(out, sessionJSON{Session: ss, Name: names[ss.Key], Duration: stats.FormatHM(float64(ss.Secs)), Live: strings.HasPrefix(ss.ID, "live:")})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func (s *Server) names() map[string]string {
	m := map[string]string{}
	for _, e := range s.core.Store.Entries() {
		m[e.Key] = e.DisplayName()
	}
	return m
}

func (s *Server) gameSessions(w http.ResponseWriter, r *http.Request) {
	e, ok := s.lookup(w, r)
	if !ok {
		return
	}
	var mine []model.Session
	for _, ss := range s.core.Sessions(true) {
		if ss.Key == e.Key {
			mine = append(mine, ss)
		}
	}
	sort.SliceStable(mine, func(i, j int) bool { return mine[i].Start.Before(mine[j].Start) })
	writeJSON(w, r, s.sessionList(r, mine, s.names()))
}

func (s *Server) gameDaily(w http.ResponseWriter, r *http.Request) {
	e, ok := s.lookup(w, r)
	if !ok {
		return
	}
	days := intParam(r, "days", 30, 1, 3660)
	now := time.Now()
	to := stats.StartOfDay(now).AddDate(0, 0, 1)
	from := to.AddDate(0, 0, -days)
	b := stats.Buckets(s.core.Sessions(true), from, to, stats.Day, s.weekStart(), func(k string) bool { return k == e.Key })
	type day struct {
		Date  string  `json:"date"`
		Secs  int64   `json:"secs"`
		Hours float64 `json:"hours"`
	}
	out := make([]day, 0, len(b))
	for _, x := range b {
		out = append(out, day{Date: stats.DayKey(x.Start), Secs: int64(math.Round(x.Secs)), Hours: hours(x.Secs)})
	}
	writeJSON(w, r, out)
}

func parseTime(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return t, nil
		}
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Unix(n, 0), nil
	}
	return time.Time{}, fmt.Errorf("bad time %q (use RFC3339, YYYY-MM-DD or unix seconds)", v)
}

func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err := parseTime(q.Get("from"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err.Error())
		return
	}
	to, err := parseTime(q.Get("to"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err.Error())
		return
	}
	key := q.Get("key")
	if key != "" {
		key = s.core.Store.Resolve(key)
	}
	filter := s.core.KindFilter(q.Get("kind"))
	var out []model.Session
	for _, ss := range s.core.Sessions(q.Get("live") != "") {
		if key != "" && ss.Key != key {
			continue
		}
		if filter != nil && !filter(ss.Key) {
			continue
		}
		if !from.IsZero() && ss.End.Before(from) {
			continue
		}
		if !to.IsZero() && !ss.Start.Before(to) {
			continue
		}
		out = append(out, ss)
	}
	writeJSON(w, r, s.sessionList(r, out, s.names()))
}

func (s *Server) summary(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	sum := stats.Summarize(s.core.Sessions(true), s.core.KindFilter(kind), time.Now(), s.weekStart())
	names := s.names()
	writeJSON(w, r, map[string]any{
		"summary": sum,
		"pretty": map[string]string{
			"total": stats.FormatHM(sum.Total), "today": stats.FormatHM(sum.Today), "week": stats.FormatHM(sum.Week),
			"month": stats.FormatHM(sum.Month), "year": stats.FormatHM(sum.Year), "dailyAverage": stats.FormatHM(sum.DailyAverage),
			"avgSession": stats.FormatHM(sum.AvgSession), "mostPlayed": names[sum.TopKey],
			"longestSession": names[sum.Longest.Key] + " (" + stats.FormatHM(float64(sum.Longest.Secs)) + ")",
		},
	})
}

func (s *Server) chart(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rng := q.Get("range")
	if rng == "" {
		rng = "30d"
	}
	sessions := s.core.Sessions(true)
	now := time.Now()
	var first time.Time
	if len(sessions) > 0 {
		first = sessions[0].Start
	}
	from, to, g := stats.Resolve(rng, now, first)
	if b := q.Get("bucket"); b != "" {
		g = stats.ParseGranularity(b)
	}
	if f, err := parseTime(q.Get("from")); err == nil && !f.IsZero() {
		from = f
	}
	if t, err := parseTime(q.Get("to")); err == nil && !t.IsZero() {
		to = t
	}
	buckets := stats.Buckets(sessions, from, to, g, s.weekStart(), s.core.KindFilter(q.Get("kind")))
	names := s.names()
	type bucket struct {
		Start  time.Time          `json:"start"`
		End    time.Time          `json:"end"`
		Label  string             `json:"label"`
		Secs   int64              `json:"secs"`
		Hours  float64            `json:"hours"`
		ByGame map[string]float64 `json:"byGameHours,omitempty"`
	}
	out := make([]bucket, 0, len(buckets))
	for _, b := range buckets {
		bb := bucket{Start: b.Start, End: b.End, Label: stats.BucketLabel(b.Start, g), Secs: int64(math.Round(b.Secs)), Hours: hours(b.Secs)}
		if q.Get("breakdown") != "" && len(b.ByKey) > 0 {
			bb.ByGame = map[string]float64{}
			for k, v := range b.ByKey {
				n := names[k]
				if n == "" {
					n = k
				}
				bb.ByGame[n] += hours(v)
			}
		}
		out = append(out, bb)
	}
	writeJSON(w, r, map[string]any{"range": rng, "bucket": g.String(), "from": from, "to": to, "buckets": out})
}

func (s *Server) top(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rng := q.Get("range")
	var from, to time.Time
	if rng != "" && rng != "all" {
		from, to, _ = stats.Resolve(rng, time.Now(), time.Time{})
	}
	limit := intParam(r, "limit", 10, 1, 1000)
	ranked := stats.Top(s.core.Sessions(true), from, to, s.core.KindFilter(q.Get("kind")), limit)
	names := s.names()
	type row struct {
		Rank     int     `json:"rank"`
		Key      string  `json:"key"`
		Name     string  `json:"name"`
		Secs     int64   `json:"secs"`
		Hours    float64 `json:"hours"`
		Total    string  `json:"total"`
		Sessions int     `json:"sessions"`
	}
	out := make([]row, 0, len(ranked))
	for i, x := range ranked {
		out = append(out, row{Rank: i + 1, Key: x.Key, Name: names[x.Key], Secs: int64(math.Round(x.Secs)), Hours: hours(x.Secs), Total: stats.FormatHM(x.Secs), Sessions: x.Sessions})
	}
	writeJSON(w, r, out)
}

func (s *Server) heatmap(w http.ResponseWriter, r *http.Request) {
	hm := stats.Heatmap(s.core.Sessions(true), time.Local, s.core.KindFilter(r.URL.Query().Get("kind")))
	days := map[string][]float64{}
	for d := 0; d < 7; d++ {
		row := make([]float64, 24)
		for h := 0; h < 24; h++ {
			row[h] = hours(hm[d][h])
		}
		days[time.Weekday(d).String()] = row
	}
	writeJSON(w, r, map[string]any{"unit": "hours", "hoursOfDay": days})
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Disposition", `attachment; filename="simpelplaytime-backup.json"`)
	writeJSON(w, r, s.core.Store.Export())
}

func (s *Server) exportCSV(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="simpelplaytime-sessions.csv"`)
	_ = s.core.Store.WriteCSV(w)
}
