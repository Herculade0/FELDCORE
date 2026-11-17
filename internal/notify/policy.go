package notify

import (
	"strconv"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/events"
	"github.com/Herculade0/feldcore/internal/store"
)

const (
	defaultDedupe = 15 * 60
	defaultRate   = 30
)

func dedupeWindow(n store.Notify) time.Duration {
	sec := n.DedupeSeconds
	if sec <= 0 {
		sec = defaultDedupe
	}
	return time.Duration(sec) * time.Second
}

func rateLimit(n store.Notify) int {
	if n.RatePerHour <= 0 {
		return defaultRate
	}
	return n.RatePerHour
}

func inAppOn(n store.Notify) bool {
	return !n.InAppSet || n.InAppEnabled
}

// matchEvent reports whether pattern selects name. "*" selects everything.
// A pattern ending in ".*" selects that prefix ("backup.*" selects
// "backup.failed"). Anything else is an exact name.
func matchEvent(pattern, name string) bool {
	pattern = strings.TrimSpace(pattern)
	name = strings.TrimSpace(name)
	switch {
	case pattern == "" || name == "":
		return false
	case pattern == "*":
		return true
	case strings.HasSuffix(pattern, ".*"):
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(name, prefix)
	default:
		return pattern == name
	}
}

func severityRank(s string) int {
	return events.Rank(events.Severity(strings.TrimSpace(s)))
}

// inQuiet reports whether now sits inside the daily window. A window that
// crosses midnight (22:00 to 07:00) is included. Times are the server's local
// clock, which is the clock the setup wizard sets.
func inQuiet(q store.NotifyQuiet, now time.Time) bool {
	if !q.Enabled {
		return false
	}
	start, ok1 := clock(q.Start)
	end, ok2 := clock(q.End)
	if !ok1 || !ok2 {
		return false
	}
	mins := now.Hour()*60 + now.Minute()
	if start == end {
		return true
	}
	if start < end {
		return mins >= start && mins < end
	}
	return mins >= start || mins < end
}

func clock(s string) (int, bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func validPattern(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" || len(p) > 64 {
		return false
	}
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '*':
		default:
			return false
		}
	}
	if strings.Count(p, "*") > 1 {
		return false
	}
	if strings.Contains(p, "*") && p != "*" && !strings.HasSuffix(p, ".*") {
		return false
	}
	return true
}
