package main

import (
	"fmt"
	"html/template"
	"os"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// things that need to be replaced with html or otherwise

var replacer = strings.NewReplacer(
	"\n", "<br>",
	"&lt;", "<span><</span>",
	"&gt;", "<span>></span>",
)

// constants that discordgo doesn't have yet
const (
	ChannelTypeForum discordgo.ChannelType = 15
)

// discordgo constants
var discord *discordgo.Session
var err error

// regexp checks
var mentionRe = regexp.MustCompile(`<@([0-9]*)>`)
var emojiRe = regexp.MustCompile(`<:([A-z]*?):([0-9]*)>`)
var numOnlyRe = regexp.MustCompile(`([^0-9])`)

// Discord Thread
func DiscordInit() {
	discord, err = discordgo.New("Bot " + LocalConfig.BotToken)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	discord.Identify.Intents = discordgo.MakeIntent(discordgo.IntentsAllWithoutPrivileged)
	discord.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		// Update the status of the bot
		fmt.Printf("Logged in as: %v#%v\n", s.State.User.Username, s.State.User.Discriminator)
	})

	discord.Open()
}

// Get the relevant channels in a guild.
type Channels struct {
	Channels []*discordgo.Channel
	Error    error
}

func GetTopics(guildID string) (topics Channels) {
	topics.Channels = make([]*discordgo.Channel, 0)
	guild, err := discord.State.Guild(guildID)
	if err != nil {
		topics.Error = err
		return
	}
	channels := guild.Channels
	for _, v := range channels {
		if v.Type == ChannelTypeForum {
			topics.Channels = append(topics.Channels, v)
		}

	}
	return
}

// Get the title of a guild.
func GetServerTitle(guildID string) string {
	guild, err := discord.Guild(guildID)
	if err != nil {
		return err.Error()
	}
	return guild.Name
}

// Get the title of a channel
func GetChannelTitle(chanID string) string {
	channel, err := GetChannel(chanID)
	if err != nil {
		return err.Error()
	}
	return channel.Name
}

// Get a channel's threads.
func GetThreadsInChannel(guildID, chanID string) (threads Channels) {
	threads.Channels = make([]*discordgo.Channel, 0)
	threads_, err := discord.GuildThreadsActive(guildID)
	if err != nil {
		threads.Error = err
		return
	}
	for _, v := range threads_.Threads {
		if v.ParentID == chanID {
			threads.Channels = append(threads.Channels, v)
		}
	}
	return
}

type Posts struct {
	Posts []discordgo.Message
	Error error
}

// Get the last 100 messages in a channel.
func GetMessagesInChannel(chanID string) (posts Posts) {
	done := false
	before := ""
	for !done {
		// get the past 100 messages in a channel.
		messages, err := discord.ChannelMessages(chanID, 100, before, "", "")
		if err != nil {
			posts.Error = err
			return
		}
		for _, v := range messages {
			posts.Posts = append([]discordgo.Message{(*v)}, posts.Posts...)
		}
		// If we were only able to get up to 100 messages,
		// search again but after the message we stopped at.
		if len(messages) >= 100 {
			before = messages[99].ID
		} else {
			done = true
		}
	}
	return
}

// Get a channel unless it's not one we should be able to view,
func GetChannel(chanID string) (*discordgo.Channel, error) {
	channel, err := discord.State.Channel(chanID)
	if err != nil {
		return nil, err
	}
	if channel.Type != ChannelTypeForum && channel.Type != discordgo.ChannelTypeGuildPublicThread {
		return nil, fmt.Errorf("Desired channel is not a forum")
	}
	return channel, nil
}

// Count all the messages in all the threads of a channel.
func PostCount(guildID, chanID string) string {
	threads := GetThreadsInChannel(guildID, chanID)
	if threads.Error != nil {
		return err.Error()
	}
	count := 0
	for _, v := range threads.Channels {
		messages := GetMessagesInChannel(v.ID)
		count += len(messages.Posts)
	}
	return fmt.Sprint(count)
}

// Analyze content for mentions and emojis
func FormatDiscordThings(guildID, content string) template.HTML {
	parts := strings.Split(content, " ")
	var newContent string
	for _, v := range parts {
		switch {
		/*case mentionRe.Match([]byte(v)):
			newContent += FormatUserMention(guildID, template.HTMLEscapeString(v)) + " "
		/*case emojiRe.Match(v):
		newContent += FormatEmojiMention(guildID, template.HTMLEscapeString(v)) + " "*/
		default:
			newContent += replacer.Replace(template.HTMLEscapeString(v)) + " "
		}
	}
	return template.HTML(Markdown(newContent))
}

func FormatUserMention(guildID, mention string) string {
	var memberID []byte
	numOnlyRe.ReplaceAll(memberID, []byte(mention))
	member, err := discord.State.Member(guildID, string(memberID))
	if err != nil {
		return "(" + err.Error() + ")"
	}
	return "@" + member.User.Username
}

// Get the number of guilds we're in.
func GuildNum() string {
	return fmt.Sprint(len(discord.State.Guilds))
}
