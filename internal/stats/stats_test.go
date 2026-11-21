package stats

import (
	"math"
	"testing"
	"time"

	"github.com/harsiz/SimpelPlaytime/internal/model"
)

var loc = time.UTC

func at(day, hour, min int) time.Time {
	return time.Date(2026, 3, day, hour, min, 0, 0, loc)
}

func s(key string, start time.Time, mins int) model.Session {
	return model.Session{Key: key, Start: start, End: start.Add(time.Duration(mins) * time.Minute), Secs: int64(mins * 60), Focus: int64(mins * 30)}
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.001 }

func TestPerEntry(t *testing.T) {
	ss := []model.Session{s("a", at(1, 10, 0), 30), s("a", at(2, 10, 0), 90), s("b", at(1, 12, 0), 10)}
	pe := PerEntry(ss)
	a := pe["a"]
	if a.Secs != 7200 || a.Sessions != 2 || a.Longest != 5400 || !a.First.Equal(at(1, 10, 0)) || !a.Last.Equal(at(2, 11, 30)) {
		t.Errorf("a = %+v", a)
	}
	if a.Average() != 3600 || !near(a.FocusRatio(), 0.5) {
		t.Errorf("avg %v focus %v", a.Average(), a.FocusRatio())
	}
	if (Totals{}).Average() != 0 || (Totals{}).FocusRatio() != 0 {
		t.Error("zero totals")
	}
}

func TestBucketsSplitAcrossMidnight(t *testing.T) {
	// 23:00 -> 01:00, 120 minutes, should split 60/60
	ss := []model.Session{s("a", at(5, 23, 0), 120), s("b", at(6, 12, 0), 30)}
	b := Buckets(ss, at(5, 0, 0), at(7, 0, 0), Day, time.Monday, nil)
	if len(b) != 2 {
		t.Fatalf("buckets = %d", len(b))
	}
	if !near(b[0].Secs, 3600) || !near(b[1].Secs, 3600+1800) || !near(b[1].ByKey["b"], 1800) {
		t.Errorf("buckets = %+v", b)
	}
	// filter
	b = Buckets(ss, at(5, 0, 0), at(7, 0, 0), Day, time.Monday, func(k string) bool { return k == "b" })
	if !near(b[0].Secs, 0) || !near(b[1].Secs, 1800) {
		t.Errorf("filtered = %+v", b)
	}
}

func TestBucketsWeekAndMonth(t *testing.T) {
	ss := []model.Session{s("a", at(2, 10, 0), 60), s("a", at(9, 10, 0), 60), s("a", at(31, 10, 0), 60)}
	// March 2 2026 is a Monday
	w := Buckets(ss, at(2, 0, 0), at(16, 0, 0), Week, time.Monday, nil)
	if len(w) != 2 || !w[0].Start.Equal(at(2, 0, 0)) || !near(w[0].Secs, 3600) || !near(w[1].Secs, 3600) {
		t.Errorf("weeks = %+v", w)
	}
	m := Buckets(ss, at(1, 0, 0), time.Date(2026, 5, 1, 0, 0, 0, 0, loc), Month, time.Monday, nil)
	if len(m) != 2 || !near(m[0].Secs, 3*3600) || !near(m[1].Secs, 0) {
		t.Errorf("months = %+v", m)
	}
}

func TestAlign(t *testing.T) {
	wed := at(4, 15, 30) // Wednesday
	if got := Align(wed, Week, time.Monday); !got.Equal(at(2, 0, 0)) {
		t.Errorf("monday week = %v", got)
	}
	if got := Align(wed, Week, time.Sunday); !got.Equal(at(1, 0, 0)) {
		t.Errorf("sunday week = %v", got)
	}
	if got := Align(wed, Month, time.Monday); !got.Equal(at(1, 0, 0)) {
		t.Errorf("month = %v", got)
	}
}

func TestDailyAndStreaks(t *testing.T) {
	ss := []model.Session{
		s("a", at(1, 10, 0), 60),
		s("a", at(2, 23, 30), 60), // 30m on the 2nd, 30m on the 3rd
		s("a", at(3, 10, 0), 60),
		s("a", at(10, 10, 0), 60),
		s("a", at(11, 10, 0), 60),
	}
	d := Daily(ss, loc, nil)
	if !near(d["2026-03-02"], 1800) || !near(d["2026-03-03"], 5400) {
		t.Errorf("daily = %v", d)
	}
	cur, long := Streaks(d, at(11, 20, 0), 60)
	if cur != 2 || long != 3 {
		t.Errorf("streaks = %d %d", cur, long)
	}
	// nothing yet today still keeps yesterday's streak alive
	cur, _ = Streaks(d, at(12, 8, 0), 60)
	if cur != 2 {
		t.Errorf("streak through yesterday = %d", cur)
	}
	cur, _ = Streaks(d, at(14, 8, 0), 60)
	if cur != 0 {
		t.Errorf("broken streak = %d", cur)
	}
	if c, l := Streaks(nil, at(1, 0, 0), 60); c != 0 || l != 0 {
		t.Error("empty streaks")
	}
}

func TestHeatmap(t *testing.T) {
	ss := []model.Session{s("a", at(2, 21, 30), 60)} // Monday 21:30 -> 22:30
	hm := Heatmap(ss, loc, nil)
	if !near(hm[time.Monday][21], 1800) || !near(hm[time.Monday][22], 1800) {
		t.Errorf("heatmap row = %v", hm[time.Monday])
	}
}

func TestTopAndBetween(t *testing.T) {
	ss := []model.Session{s("a", at(1, 10, 0), 60), s("b", at(1, 12, 0), 120), s("a", at(5, 10, 0), 30)}
	top := Top(ss, time.Time{}, time.Time{}, nil, 0)
	if len(top) != 2 || top[0].Key != "b" || !near(top[1].Secs, 5400) || top[1].Sessions != 2 {
		t.Errorf("top = %+v", top)
	}
	top = Top(ss, at(4, 0, 0), at(6, 0, 0), nil, 1)
	if len(top) != 1 || top[0].Key != "a" || !near(top[0].Secs, 1800) {
		t.Errorf("ranged top = %+v", top)
	}
	if got := Between(ss, at(1, 10, 30), at(1, 13, 0), nil); !near(got, 1800+3600) {
		t.Errorf("between = %v", got)
	}
}

func TestSummarize(t *testing.T) {
	now := at(11, 20, 0) // Wednesday
	ss := []model.Session{
		s("a", at(1, 10, 0), 60),
		s("b", at(9, 1, 0), 120), // Monday night owl
		s("a", at(10, 10, 0), 60),
		s("a", at(11, 10, 0), 30),
	}
	sum := Summarize(ss, nil, now, time.Monday)
	if !near(sum.Total, 270*60) || sum.Sessions != 4 || sum.UniqueEntries != 2 {
		t.Errorf("totals = %+v", sum)
	}
	if !near(sum.Today, 1800) || !near(sum.Yesterday, 3600) || !near(sum.Week, 210*60) || !near(sum.Month, 270*60) {
		t.Errorf("periods today=%v yest=%v week=%v month=%v", sum.Today, sum.Yesterday, sum.Week, sum.Month)
	}
	if sum.TopKey != "a" || sum.Longest.Key != "b" || sum.CurrentStreak != 3 || sum.LongestStreak != 3 {
		t.Errorf("top=%s longest=%s streak=%d/%d", sum.TopKey, sum.Longest.Key, sum.CurrentStreak, sum.LongestStreak)
	}
	if sum.BestDay != "2026-03-09" || sum.FavWeekday != "Monday" || sum.FavHour != 10 {
		t.Errorf("best day %s weekday %s hour %d", sum.BestDay, sum.FavWeekday, sum.FavHour)
	}
	if !near(sum.NightOwlShare, 120.0/270.0) || !near(sum.FocusRatio, 0.5) || sum.ActiveDays != 4 {
		t.Errorf("night %v focus %v active %d", sum.NightOwlShare, sum.FocusRatio, sum.ActiveDays)
	}
	empty := Summarize(nil, nil, now, time.Monday)
	if empty.Sessions != 0 || empty.Total != 0 {
		t.Error("empty summary")
	}
}

func TestResolve(t *testing.T) {
	now := at(15, 12, 0)
	from, to, g := Resolve("7d", now, time.Time{})
	if !from.Equal(at(9, 0, 0)) || !to.Equal(at(16, 0, 0)) || g != Day {
		t.Errorf("7d = %v %v %v", from, to, g)
	}
	_, _, g = Resolve("12m", now, time.Time{})
	if g != Month {
		t.Error("12m should be monthly")
	}
	from, _, g = Resolve("all", now, time.Date(2024, 1, 5, 3, 0, 0, 0, loc))
	if !from.Equal(time.Date(2024, 1, 5, 0, 0, 0, 0, loc)) || g != Month {
		t.Errorf("all = %v %v", from, g)
	}
	from, _, _ = Resolve("bogus", now, time.Time{})
	if !from.Equal(at(15, 0, 0).AddDate(0, 0, -29)) {
		t.Errorf("default = %v", from)
	}
	if ParseGranularity("week") != Week || ParseGranularity("x") != Day || Month.String() != "month" {
		t.Error("granularity parse")
	}
}

func TestFormatting(t *testing.T) {
	hm := map[float64]string{0: "0m", 30: "30s", 59 * 60: "59m", 27*3600 + 12*60: "27h 12m", 3600: "1h", 1234 * 3600: "1,234h"}
	for in, want := range hm {
		if got := FormatHM(in); got != want {
			t.Errorf("FormatHM(%v) = %q, want %q", in, got, want)
		}
	}
	if FormatClock(4364) != "1:12:44" || FormatClock(59) != "0:59" {
		t.Errorf("clock = %s %s", FormatClock(4364), FormatClock(59))
	}
	if FormatDecimal(27.2*3600) != "27.2h" || Format(3600, "hm") != "1h" || Format(3600, "decimal") != "1.0h" {
		t.Error("decimal format")
	}
	cases := []struct {
		secs      float64
		val, unit string
	}{{0, "-", "hours"}, {105 * 60, "1", "hour"}, {50 * 60, "50", "mins"}, {111 * 3600, "111", "hours"}, {20, "1", "min"}}
	for _, c := range cases {
		v, u := FormatShort(c.secs)
		if v != c.val || u != c.unit {
			t.Errorf("FormatShort(%v) = %s %s", c.secs, v, u)
		}
	}
	now := at(10, 12, 0)
	agos := map[time.Time]string{
		{}:                         "never",
		now.Add(-10 * time.Second): "just now",
		now.Add(-5 * time.Minute):  "5m ago",
		now.Add(-3 * time.Hour):    "3h ago",
		at(9, 20, 0):               "yesterday",
		at(7, 12, 0):               "3d ago",
		at(1, 12, 0):               "1w ago",
	}
	for in, want := range agos {
		if got := Ago(in, now); got != want {
			t.Errorf("Ago(%v) = %q, want %q", in, got, want)
		}
	}
	if top, step := NiceCeil(7.3); top != 8 || step != 2 {
		t.Errorf("NiceCeil = %v %v", top, step)
	}
	if top, _ := NiceCeil(0); top != 1 {
		t.Error("NiceCeil(0)")
	}
	if HourLabel(0) != "12 AM" || HourLabel(13) != "1 PM" || HourLabel(12) != "12 PM" {
		t.Error("hour labels")
	}
	if BucketLabel(at(2, 0, 0), Day) != "Mon 2" || BucketLabel(time.Date(2026, 1, 1, 0, 0, 0, 0, loc), Month) != "Jan 26" {
		t.Error("bucket labels")
	}
}
