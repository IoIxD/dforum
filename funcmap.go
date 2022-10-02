package main

import (
	"github.com/diamondburned/arikawa/v3/discord"
)

var funcMap = map[string]any{
	"TrimForMeta":  TrimForMeta,
	"IsPostPinned": IsPostPinned,
}

// Trim a string to 128 characters, for meta tags.
func TrimForMeta(value string) string {
	if len(value) <= 127 {
		return value
	}
	return value[:128] + "..."
}

func IsPostPinned(post discord.Channel) bool {
	return post.Flags&discord.PinnedThread != 0
}
