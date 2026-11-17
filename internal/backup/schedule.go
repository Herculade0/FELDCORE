package backup

import (
	"strings"
	"time"
)

var weekdayName = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

// scheduleDue reports whether dest should start a backup at now.
func scheduleDue(mode, clock, day string, last, now time.Time) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "manual":
		return false, nil
	case "daily":
		return Due(clock, last, now)
	case "weekly":
		h, m, err := parseClock(clock)
		if err != nil {
			return false, err
		}
		want, err := parseWeekday(day)
		if err != nil {
			return false, err
		}
		if int(now.Weekday()) != want {
			return false, nil
		}
		slot := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
		if now.Before(slot) {
			return false, nil
		}
		return last.IsZero() || last.Before(slot), nil
	default:
		return false, input("The schedule is manual, daily, or weekly.")
	}
}

func parseWeekday(day string) (int, error) {
	day = strings.ToLower(strings.TrimSpace(day))
	for i, name := range weekdayName {
		if day == name || day == name[:3] {
			return i, nil
		}
	}
	return 0, input("Pick a day of the week for a weekly backup.")
}

func cleanSchedule(mode, clock, day string) (string, string, string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "manual"
	}
	switch mode {
	case "manual":
		return "manual", "", "", nil
	case "daily":
		if _, _, err := parseClock(clock); err != nil {
			return "", "", "", err
		}
		return "daily", strings.TrimSpace(clock), "", nil
	case "weekly":
		if _, _, err := parseClock(clock); err != nil {
			return "", "", "", err
		}
		n, err := parseWeekday(day)
		if err != nil {
			return "", "", "", err
		}
		return "weekly", strings.TrimSpace(clock), weekdayName[n], nil
	default:
		return "", "", "", input("The schedule is manual, daily, or weekly.")
	}
}
