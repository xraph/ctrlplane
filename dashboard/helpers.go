package dashboard

import (
	"fmt"
	"strconv"
	"time"

	"github.com/xraph/forgeui/components/card"
)

// cardProps returns the standard card props with rounded-sm styling.
func cardProps() card.Props {
	return card.Props{Class: "rounded-sm"}
}

// formatTimeAgo formats a time as a human-readable relative string.
func formatTimeAgo(t time.Time) string {
	d := time.Since(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}

		return strconv.Itoa(mins) + " minutes ago"
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour ago"
		}

		return strconv.Itoa(hours) + " hours ago"
	case d < 30*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}

		return strconv.Itoa(days) + " days ago"
	default:
		return t.Format("Jan 02, 2006")
	}
}

// formatDate formats a time as a date string.
func formatDate(t time.Time) string {
	return t.Format("Jan 02, 2006")
}

// formatDateTime formats a time as a date+time string.
func formatDateTime(t time.Time) string {
	return t.Format("Jan 02, 2006 15:04")
}

// truncateString truncates a string to the given max length, adding "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	return s[:maxLen-3] + "..."
}

// percentUsed calculates the percentage of used vs limit, capped at 100.
func percentUsed(used, limit int) int {
	if limit <= 0 {
		return 0
	}

	pct := (used * 100) / limit
	if pct > 100 {
		return 100
	}

	return pct
}

// formatDuration formats a duration to a human-readable string.
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}

	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}

	if d < time.Hour {
		return fmt.Sprintf("%.0fm %.0fs", d.Minutes(), d.Seconds()-d.Minutes()*60)
	}

	return fmt.Sprintf("%.0fh %.0fm", d.Hours(), d.Minutes()-d.Hours()*60)
}

// boolPtr returns a pointer to a bool value.
func boolPtr(b bool) *bool {
	return &b
}
