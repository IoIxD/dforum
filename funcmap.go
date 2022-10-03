package main

var funcMap = map[string]any{
	"TrimForMeta": TrimForMeta,
}

// Trim a string to 128 characters, for meta tags.
func TrimForMeta(value string) string {
	if len(value) <= 127 {
		return value
	}
	return value[:128] + "..."
}
