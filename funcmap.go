package main

import "html/template"

var FuncMap = template.FuncMap{
	"GetTopics":            GetTopics,
	"GetServerTitle":       GetServerTitle,
	"GetThreadsInChannel":  GetThreadsInChannel,
	"GetChannelTitle":      GetChannelTitle,
	"GetMessagesInChannel": GetMessagesInChannel,
	"PostCount":            PostCount,
	"PrettyTime":           PrettyTime,
	"FormatDiscordThings":  FormatDiscordThings,
	"GuildNum":             GuildNum,
}
