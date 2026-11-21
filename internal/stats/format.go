package stats

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// FormatHM formats seconds like "27h 12m", "45m" or "30s".
func FormatHM(secs float64) string {
	if secs < 0 {
		secs = 0
	}
	total := int64(math.Round(secs))
	if total < 60 {
		if total == 0 {
			return "0m"
		}
		return fmt.Sprintf("%ds", total)
	}
	h := total / 3600
	m := (total % 3600) / 60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	if m == 0 {
		return fmt.Sprintf("%sh", thousands(h))
	}
	return fmt.Sprintf("%sh %dm", thousands(h), m)
}

// FormatDecimal formats seconds as hours with one decimal ("27.2h").
func FormatDecimal(secs float64) string {
	h := secs / 3600
	if h < 0.05 && secs > 0 {
		return FormatHM(secs)
	}
	return strconv.FormatFloat(math.Round(h*10)/10, 'f', 1, 64) + "h"
}

// Format picks the style by name ("hm" or "decimal").
func Format(secs float64, style string) string {
	if style == "decimal" {
		return FormatDecimal(secs)
	}
	return FormatHM(secs)
}

// FormatClock formats seconds as a running clock "1:12:44" (or "12:44").
func FormatClock(secs int64) string {
	if secs < 0 {
		secs = 0
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	s := secs % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// FormatShort returns a big number and a unit, like ("111", "hours") or
// ("105", "mins"), the style used on library cards.
func FormatShort(secs float64) (string, string) {
	if secs <= 0 {
		return "-", "hours"
	}
	if secs < 3600 {
		m := int64(math.Round(secs / 60))
		if m < 1 {
			m = 1
		}
		if m == 1 {
			return "1", "min"
		}
		return strconv.FormatInt(m, 10), "mins"
	}
	h := int64(math.Floor(secs / 3600))
	if h == 1 {
		return "1", "hour"
	}
	return thousands(h), "hours"
}

func thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// Ago formats a time relative to now ("just now", "5m ago", "yesterday").
func Ago(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour && StartOfDay(t).Equal(StartOfDay(now)):
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	days := int(StartOfDay(now).Sub(StartOfDay(t)).Hours()/24 + 0.5)
	switch {
	case days <= 1:
		return "yesterday"
	case days < 7:
		return fmt.Sprintf("%dd ago", days)
	case days < 30:
		return fmt.Sprintf("%dw ago", days/7)
	case days < 365:
		return fmt.Sprintf("%dmo ago", days/30)
	}
	return fmt.Sprintf("%dy ago", days/365)
}

// NiceCeil rounds a chart maximum (in hours) up to a nice number and returns
// it together with a tick step.
func NiceCeil(maxHours float64) (top, step float64) {
	if maxHours <= 0 {
		return 1, 0.25
	}
	steps := []float64{0.25, 0.5, 1, 2, 5, 10, 20, 25, 50, 100, 200, 250, 500, 1000, 2000, 5000}
	for _, s := range steps {
		if maxHours/s <= 5 {
			return math.Ceil(maxHours/s) * s, s
		}
	}
	s := math.Pow(10, math.Ceil(math.Log10(maxHours/5)))
	return math.Ceil(maxHours/s) * s, s
}

// HourLabel formats an hour of the day like "9 PM".
func HourLabel(h int) string {
	switch {
	case h == 0:
		return "12 AM"
	case h < 12:
		return fmt.Sprintf("%d AM", h)
	case h == 12:
		return "12 PM"
	}
	return fmt.Sprintf("%d PM", h-12)
}

// BucketLabel returns a short axis label for a bucket start.
func BucketLabel(t time.Time, g Granularity) string {
	switch g {
	case Week:
		return t.Format("Jan 2")
	case Month:
		if t.Month() == time.January {
			return t.Format("Jan 06")
		}
		return t.Format("Jan")
	}
	return t.Format("Mon 2")
}
