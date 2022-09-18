package main

import (
	"html/template"

	"github.com/disgoorg/snowflake/v2"
)

func FuncMap(b *Bot) template.FuncMap {
	return template.FuncMap{
		"GetAvatarURL":					b.GetAvatarURL,
		"GetForums":            b.GetForums,
		"GetGuildName":         b.GetGuildName,
		"GetThreadsInChannel":  b.GetThreadsInChannel,
		"GetChannelTitle":      b.GetChannelTitle,
		"GetMessagesInChannel": b.GetMessagesInChannel,
		"PostCount":            b.PostCount,
		"PrettyTime":           PrettyTime,
		"FormatDiscordThings":  b.FormatDiscordThings,
		"GuildNum":             b.GuildNum,
		"ParseSnowflake":       snowflake.MustParse,
	}
}
