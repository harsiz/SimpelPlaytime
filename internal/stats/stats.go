// Package stats turns sessions into totals, chart buckets, streaks, heatmaps
// and summaries. Everything here is pure (no IO) so it is easy to test.
package stats

import (
	"sort"
	"time"

	"github.com/harsiz/SimpelPlaytime/internal/model"
)

// Filter keeps the sessions of the keys it returns true for. A nil filter
// keeps everything.
type Filter func(key string) bool

func keep(f Filter, key string) bool { return f == nil || f(key) }

// Totals are the lifetime numbers of one entry.
type Totals struct {
	Secs     int64     `json:"secs"`
	Focus    int64     `json:"focus"`
	Sessions int       `json:"sessions"`
	Longest  int64     `json:"longest"`
	First    time.Time `json:"first"`
	Last     time.Time `json:"last"`
}

// Average returns the average session length in seconds.
func (t Totals) Average() float64 {
	if t.Sessions == 0 {
		return 0
	}
	return float64(t.Secs) / float64(t.Sessions)
}

// FocusRatio is the share of playtime the app was in the foreground.
func (t Totals) FocusRatio() float64 {
	if t.Secs == 0 {
		return 0
	}
	return float64(t.Focus) / float64(t.Secs)
}

// PerEntry adds up sessions per key.
func PerEntry(sessions []model.Session) map[string]Totals {
	out := map[string]Totals{}
	for _, s := range sessions {
		t := out[s.Key]
		t.Secs += s.Secs
		t.Focus += s.Focus
		t.Sessions++
		if s.Secs > t.Longest {
			t.Longest = s.Secs
		}
		if t.First.IsZero() || s.Start.Before(t.First) {
			t.First = s.Start
		}
		if s.End.After(t.Last) {
			t.Last = s.End
		}
		out[s.Key] = t
	}
	return out
}

// Granularity of chart buckets.
type Granularity int

const (
	Day Granularity = iota
	Week
	Month
)

func (g Granularity) String() string {
	switch g {
	case Week:
		return "week"
	case Month:
		return "month"
	}
	return "day"
}

// ParseGranularity parses "day", "week" or "month" (default day).
func ParseGranularity(s string) Granularity {
	switch s {
	case "week", "weekly", "w":
		return Week
	case "month", "monthly", "m":
		return Month
	}
	return Day
}

// StartOfDay returns local midnight of t (in t's location).
func StartOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// Align returns the start of the bucket that contains t.
func Align(t time.Time, g Granularity, weekStart time.Weekday) time.Time {
	d := StartOfDay(t)
	switch g {
	case Week:
		back := (int(d.Weekday()) - int(weekStart) + 7) % 7
		return d.AddDate(0, 0, -back)
	case Month:
		return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, d.Location())
	}
	return d
}

// Next returns the start of the bucket after the one starting at t.
func Next(t time.Time, g Granularity) time.Time {
	switch g {
	case Week:
		return t.AddDate(0, 0, 7)
	case Month:
		return t.AddDate(0, 1, 0)
	}
	return t.AddDate(0, 0, 1)
}

// Bucket is one bar of a chart.
type Bucket struct {
	Start time.Time          `json:"start"`
	End   time.Time          `json:"end"`
	Secs  float64            `json:"secs"`
	ByKey map[string]float64 `json:"byKey,omitempty"`
}

// Buckets splits playtime into day/week/month buckets between from and to.
// Sessions crossing a boundary are split proportionally.
func Buckets(sessions []model.Session, from, to time.Time, g Granularity, weekStart time.Weekday, filter Filter) []Bucket {
	start := Align(from, g, weekStart)
	var buckets []Bucket
	for b := start; b.Before(to); b = Next(b, g) {
		buckets = append(buckets, Bucket{Start: b, End: Next(b, g), ByKey: map[string]float64{}})
		if len(buckets) > 5000 {
			break
		}
	}
	if len(buckets) == 0 {
		return nil
	}
	for _, s := range sessions {
		if !keep(filter, s.Key) || s.Secs <= 0 {
			continue
		}
		addToBuckets(buckets, s)
	}
	return buckets
}

func addToBuckets(buckets []Bucket, s model.Session) {
	span := s.End.Sub(s.Start)
	total := float64(s.Secs)
	if span <= 0 {
		i := sort.Search(len(buckets), func(i int) bool { return buckets[i].End.After(s.Start) })
		if i < len(buckets) && !s.Start.Before(buckets[i].Start) {
			buckets[i].Secs += total
			buckets[i].ByKey[s.Key] += total
		}
		return
	}
	i := sort.Search(len(buckets), func(i int) bool { return buckets[i].End.After(s.Start) })
	for ; i < len(buckets) && buckets[i].Start.Before(s.End); i++ {
		lo, hi := maxTime(s.Start, buckets[i].Start), minTime(s.End, buckets[i].End)
		if !hi.After(lo) {
			continue
		}
		part := total * float64(hi.Sub(lo)) / float64(span)
		buckets[i].Secs += part
		buckets[i].ByKey[s.Key] += part
	}
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// DayKey formats a date as "2006-01-02" in t's location.
func DayKey(t time.Time) string { return t.Format("2006-01-02") }

// Daily returns seconds played per local day ("2006-01-02").
func Daily(sessions []model.Session, loc *time.Location, filter Filter) map[string]float64 {
	out := map[string]float64{}
	for _, s := range sessions {
		if !keep(filter, s.Key) || s.Secs <= 0 {
			continue
		}
		st, en := s.Start.In(loc), s.End.In(loc)
		if !en.After(st) {
			out[DayKey(st)] += float64(s.Secs)
			continue
		}
		span := float64(en.Sub(st))
		for d := StartOfDay(st); d.Before(en); d = d.AddDate(0, 0, 1) {
			lo, hi := maxTime(st, d), minTime(en, d.AddDate(0, 0, 1))
			if hi.After(lo) {
				out[DayKey(d)] += float64(s.Secs) * float64(hi.Sub(lo)) / span
			}
		}
	}
	return out
}

// Streaks returns the current streak (days in a row up to today, or up to
// yesterday if nothing was played yet today) and the longest streak ever.
// A day counts when at least minSecs were played.
func Streaks(daily map[string]float64, today time.Time, minSecs float64) (current, longest int) {
	var days []string
	for k, v := range daily {
		if v >= minSecs {
			days = append(days, k)
		}
	}
	if len(days) == 0 {
		return 0, 0
	}
	sort.Strings(days)
	set := map[string]bool{}
	for _, d := range days {
		set[d] = true
	}
	run := 0
	var prev time.Time
	for _, d := range days {
		t, err := time.ParseInLocation("2006-01-02", d, today.Location())
		if err != nil {
			continue
		}
		if !prev.IsZero() && StartOfDay(prev.AddDate(0, 0, 1)).Equal(t) {
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
		prev = t
	}
	d := StartOfDay(today)
	if !set[DayKey(d)] {
		d = d.AddDate(0, 0, -1)
	}
	for set[DayKey(d)] {
		current++
		d = d.AddDate(0, 0, -1)
	}
	return current, longest
}

// Heatmap returns seconds played per weekday (Sunday = 0) and hour of day.
func Heatmap(sessions []model.Session, loc *time.Location, filter Filter) [7][24]float64 {
	var out [7][24]float64
	for _, s := range sessions {
		if !keep(filter, s.Key) || s.Secs <= 0 {
			continue
		}
		st, en := s.Start.In(loc), s.End.In(loc)
		if !en.After(st) {
			out[st.Weekday()][st.Hour()] += float64(s.Secs)
			continue
		}
		span := float64(en.Sub(st))
		h := time.Date(st.Year(), st.Month(), st.Day(), st.Hour(), 0, 0, 0, loc)
		for steps := 0; h.Before(en) && steps < 24*400; steps++ {
			next := h.Add(time.Hour)
			lo, hi := maxTime(st, h), minTime(en, next)
			if hi.After(lo) {
				lt := lo.In(loc)
				out[lt.Weekday()][lt.Hour()] += float64(s.Secs) * float64(hi.Sub(lo)) / span
			}
			h = next
		}
	}
	return out
}

// Ranked is one row of a top list.
type Ranked struct {
	Key      string  `json:"key"`
	Secs     float64 `json:"secs"`
	Sessions int     `json:"sessions"`
}

// Top returns the most played keys between from and to (zero times mean no
// limit), at most n of them (n <= 0 means all).
func Top(sessions []model.Session, from, to time.Time, filter Filter, n int) []Ranked {
	agg := map[string]*Ranked{}
	for _, s := range sessions {
		if !keep(filter, s.Key) || s.Secs <= 0 {
			continue
		}
		secs := float64(s.Secs)
		if !from.IsZero() || !to.IsZero() {
			secs = overlapSecs(s, from, to)
			if secs <= 0 {
				continue
			}
		}
		r := agg[s.Key]
		if r == nil {
			r = &Ranked{Key: s.Key}
			agg[s.Key] = r
		}
		r.Secs += secs
		r.Sessions++
	}
	out := make([]Ranked, 0, len(agg))
	for _, r := range agg {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Secs != out[j].Secs {
			return out[i].Secs > out[j].Secs
		}
		return out[i].Key < out[j].Key
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// overlapSecs returns the share of a session's played seconds that falls
// between from and to (zero means open ended).
func overlapSecs(s model.Session, from, to time.Time) float64 {
	lo, hi := s.Start, s.End
	if !from.IsZero() && from.After(lo) {
		lo = from
	}
	if !to.IsZero() && to.Before(hi) {
		hi = to
	}
	if !s.End.After(s.Start) {
		if (from.IsZero() || !s.Start.Before(from)) && (to.IsZero() || s.Start.Before(to)) {
			return float64(s.Secs)
		}
		return 0
	}
	if !hi.After(lo) {
		return 0
	}
	return float64(s.Secs) * float64(hi.Sub(lo)) / float64(s.End.Sub(s.Start))
}

// Between returns the played seconds between from and to.
func Between(sessions []model.Session, from, to time.Time, filter Filter) float64 {
	total := 0.0
	for _, s := range sessions {
		if keep(filter, s.Key) {
			total += overlapSecs(s, from, to)
		}
	}
	return total
}

// Summary is the big pile of numbers shown on the stats page.
type Summary struct {
	Total         float64       `json:"totalSecs"`
	Today         float64       `json:"todaySecs"`
	Yesterday     float64       `json:"yesterdaySecs"`
	Week          float64       `json:"weekSecs"`
	LastWeek      float64       `json:"lastWeekSecs"`
	Month         float64       `json:"monthSecs"`
	Year          float64       `json:"yearSecs"`
	Sessions      int           `json:"sessions"`
	AvgSession    float64       `json:"avgSessionSecs"`
	Longest       model.Session `json:"longestSession"`
	ActiveDays    int           `json:"activeDays"`
	DailyAverage  float64       `json:"dailyAverageSecs"`
	CurrentStreak int           `json:"currentStreak"`
	LongestStreak int           `json:"longestStreak"`
	BestDay       string        `json:"bestDay,omitempty"`
	BestDaySecs   float64       `json:"bestDaySecs"`
	TopKey        string        `json:"topKey,omitempty"`
	TopSecs       float64       `json:"topSecs"`
	FavWeekday    string        `json:"favoriteWeekday,omitempty"`
	FavHour       int           `json:"favoriteHour"`
	UniqueEntries int           `json:"uniqueEntries"`
	FocusRatio    float64       `json:"focusRatio"`
	First         time.Time     `json:"firstSession"`
	NightOwlShare float64       `json:"nightOwlShare"` // share of time played 00:00-05:00
}

// Summarize computes a Summary at time now.
func Summarize(sessions []model.Session, filter Filter, now time.Time, weekStart time.Weekday) Summary {
	var sum Summary
	loc := now.Location()
	today := StartOfDay(now)
	weekStartT := Align(now, Week, weekStart)
	monthStart := Align(now, Month, weekStart)
	yearStart := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, loc)
	far := now.Add(24 * time.Hour)

	var focus float64
	keys := map[string]float64{}
	for _, s := range sessions {
		if !keep(filter, s.Key) || s.Secs <= 0 {
			continue
		}
		sum.Total += float64(s.Secs)
		focus += float64(s.Focus)
		sum.Sessions++
		keys[s.Key] += float64(s.Secs)
		if s.Secs > sum.Longest.Secs {
			sum.Longest = s
		}
		if sum.First.IsZero() || s.Start.Before(sum.First) {
			sum.First = s.Start
		}
	}
	if sum.Sessions == 0 {
		return sum
	}
	sum.Today = Between(sessions, today, far, filter)
	sum.Yesterday = Between(sessions, today.AddDate(0, 0, -1), today, filter)
	sum.Week = Between(sessions, weekStartT, far, filter)
	sum.LastWeek = Between(sessions, weekStartT.AddDate(0, 0, -7), weekStartT, filter)
	sum.Month = Between(sessions, monthStart, far, filter)
	sum.Year = Between(sessions, yearStart, far, filter)
	sum.AvgSession = sum.Total / float64(sum.Sessions)
	sum.UniqueEntries = len(keys)
	if sum.Total > 0 {
		sum.FocusRatio = focus / sum.Total
	}
	for k, v := range keys {
		if v > sum.TopSecs || v == sum.TopSecs && k < sum.TopKey {
			sum.TopKey, sum.TopSecs = k, v
		}
	}

	daily := Daily(sessions, loc, filter)
	for d, v := range daily {
		if v >= 60 {
			sum.ActiveDays++
		}
		if v > sum.BestDaySecs || v == sum.BestDaySecs && d < sum.BestDay {
			sum.BestDay, sum.BestDaySecs = d, v
		}
	}
	if sum.ActiveDays > 0 {
		sum.DailyAverage = sum.Total / float64(sum.ActiveDays)
	}
	sum.CurrentStreak, sum.LongestStreak = Streaks(daily, now, 60)

	hm := Heatmap(sessions, loc, filter)
	var byDay [7]float64
	var byHour [24]float64
	night := 0.0
	for d := 0; d < 7; d++ {
		for h := 0; h < 24; h++ {
			byDay[d] += hm[d][h]
			byHour[h] += hm[d][h]
			if h < 5 {
				night += hm[d][h]
			}
		}
	}
	best := 0
	for d := 1; d < 7; d++ {
		if byDay[d] > byDay[best] {
			best = d
		}
	}
	if byDay[best] > 0 {
		sum.FavWeekday = time.Weekday(best).String()
	}
	bh := 0
	for h := 1; h < 24; h++ {
		if byHour[h] > byHour[bh] {
			bh = h
		}
	}
	sum.FavHour = bh
	if sum.Total > 0 {
		sum.NightOwlShare = night / sum.Total
	}
	return sum
}

// Range is a preset time span for charts.
type Range struct {
	ID    string
	Label string
}

// Ranges are the presets offered in the UI and the API.
var Ranges = []Range{
	{"7d", "Last 7 days"},
	{"30d", "Last 30 days"},
	{"90d", "Last 90 days"},
	{"year", "This year"},
	{"12m", "Last 12 months"},
	{"all", "All time"},
}

// Resolve turns a range id into from/to and a sensible granularity. first
// is the time of the very first session (used by "all").
func Resolve(id string, now time.Time, first time.Time) (from, to time.Time, g Granularity) {
	today := StartOfDay(now)
	to = today.AddDate(0, 0, 1)
	switch id {
	case "7d":
		return today.AddDate(0, 0, -6), to, Day
	case "90d":
		return today.AddDate(0, 0, -89), to, Week
	case "year":
		return time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location()), to, Week
	case "12m":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, -11, 0), to, Month
	case "all":
		if first.IsZero() || first.After(now) {
			first = today.AddDate(0, 0, -29)
		}
		from = StartOfDay(first.In(now.Location()))
		days := to.Sub(from).Hours() / 24
		switch {
		case days <= 45:
			g = Day
		case days <= 400:
			g = Week
		default:
			g = Month
		}
		return from, to, g
	}
	return today.AddDate(0, 0, -29), to, Day
}
