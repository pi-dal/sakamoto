package tui

import (
	"fmt"
	"time"

	"github.com/sagernet/sing-box/daemon"
)

func testAge(timestamp int64, now time.Time) string {
	if timestamp <= 0 {
		return "undated"
	}
	age := now.Sub(time.Unix(timestamp, 0))
	if age < 0 {
		return "clock ahead"
	}
	switch {
	case age < time.Second:
		return "now"
	case age < time.Minute:
		return fmt.Sprintf("%ds ago", int(age/time.Second))
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age/time.Minute))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(age/(24*time.Hour)))
	}
}

func latencySnapshot(item *daemon.GroupItem, now time.Time, staleAfter time.Duration) string {
	if item.UrlTestDelay <= 0 {
		return "Untested"
	}
	if item.UrlTestTime > 0 && now.Sub(time.Unix(item.UrlTestTime, 0)) > staleAfter {
		return fmt.Sprintf("%dms · stale", item.UrlTestDelay)
	}
	return fmt.Sprintf("%dms · %s", item.UrlTestDelay, testAge(item.UrlTestTime, now))
}
