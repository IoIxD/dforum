package main

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

var funcMap = map[string]any{
	"PrettyTimeSince": PrettyTimeSince,
	"Capitalize":      Capitalize,
}

func Capitalize(value string) string {
	// Treat dashes as spaces
	value = strings.Replace(value, "-", " ", 99)
	valuesplit := strings.Split(value, " ")
	result := bytes.NewBuffer(nil)
	for i, v := range valuesplit {
		if len(v) <= 0 {
			continue
		}
		result.Write([]byte(strings.ToUpper(v[:1])))
		result.Write([]byte(v[1:]))
		if len(valuesplit)-1 > i {
			result.Write([]byte(" "))
		}
	}
	return result.String()
}

func PrettyTimeSince(timestamp time.Time) string {
	dur := time.Since(timestamp)
	switch {
	case dur < time.Second:
		return "just now"
	case dur < time.Second*2:
		return "1 second ago"
	case dur < time.Minute:
		return fmt.Sprintf("%d seconds ago", dur/time.Second)
	case dur < time.Minute*2:
		return "1 minute ago"
	case dur < time.Hour:
		return fmt.Sprintf("%d minutes ago", dur/time.Minute)
	case dur < time.Hour*2:
		return "1 hour ago"
	case dur < time.Hour*24:
		return fmt.Sprintf("%d hours ago", dur/time.Hour)
	case dur < time.Hour*24*2:
		return "1 day ago"
	case dur < time.Hour*24*7:
		return fmt.Sprintf("%d days ago", dur/(time.Hour*24))
	case dur < time.Hour*24*7*2:
		return "1 week ago"
	case dur < time.Hour*24*7*4:
		return fmt.Sprintf("%d weeks ago", dur/(time.Hour*24*7))
	case dur < time.Hour*24*7*4*2:
		return "1 month ago"
	case dur < time.Hour*24*7*4*12:
		return fmt.Sprintf("%d months ago", dur/(time.Hour*24*7*4))
	case dur < time.Hour*24*7*4*12*2:
		return "1 year ago"
	default:
		return fmt.Sprintf("%d years ago", dur/(time.Hour*24*7*4*12))
	}
}
