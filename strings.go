package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/gomarkdown/markdown"
)

// Capitalize a string
func Capitalize(value string) string {
	// Treat dashes as spaces
	value = strings.Replace(value, "-", " ", 99)
	valuesplit := strings.Split(value, " ")
	var result string
	for _, v := range valuesplit {
		if len(v) <= 0 {
			continue
		}
		result += strings.ToUpper(v[:1])
		result += v[1:] + " "
	}
	return result
}

// Trim a string to 128 characters, for meta tags.
func TrimForMeta(value string) string {
	if len(value) <= 127 {
		return value
	}
	return value[:128] + "..."
}

// Parsing a markdown string.

func Markdown(val string) []byte {
	return markdown.ToHTML([]byte(val), nil, nil)
}

// Function for formatting a timestamp as "x hours ago"
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
