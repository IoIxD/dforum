package main

import (
	"fmt"
	"time"
)

var funcMap = map[string]any{
	"PrettyTime": PrettyTime,
}

func PrettyTime(timestamp time.Time) string {
	unixTimeDur := time.Now().Sub(timestamp)

	if unixTimeDur.Hours() >= 8760 {
		return fmt.Sprintf("%0.f years ago", unixTimeDur.Hours()/8760)
	}
	if unixTimeDur.Hours() >= 730 {
		return fmt.Sprintf("%0.f months ago", unixTimeDur.Hours()/730)
	}
	if unixTimeDur.Hours() >= 168 {
		return fmt.Sprintf("%0.f weeks ago", unixTimeDur.Hours()/168)
	}
	if unixTimeDur.Hours() >= 24 {
		return fmt.Sprintf("%0.f days ago", unixTimeDur.Hours()/24)
	}
	if unixTimeDur.Hours() >= 1 {
		return fmt.Sprintf("%0.f hours ago", unixTimeDur.Hours())
	}
	if unixTimeDur.Minutes() >= 1 {
		return fmt.Sprintf("%0.f minutes ago", unixTimeDur.Minutes())
	}
	return fmt.Sprintf("%0.f seconds ago", unixTimeDur.Seconds())
}
