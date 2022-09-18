package main

import (
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

type Bot struct {
	Client bot.Client
}

// Get a user's avatar URL.
func (b *Bot) GetAvatarURL(user discord.User) string {
	return user.EffectiveAvatarURL()
}

// get the fourum channels in a guild.
func (b *Bot) GetForums(guildID snowflake.ID) []discord.GuildForumChannel {
	var forums []discord.GuildForumChannel
	b.Client.Caches().Channels().ForEach(func(channel discord.Channel) {
		guildForumChannel, ok := channel.(discord.GuildForumChannel)
		if !ok || guildForumChannel.GuildID() != guildID {
			return
		}
		forums = append(forums, guildForumChannel)
	})
	return forums
}

// Get the title of a guild.
func (b *Bot) GetGuildName(guildID snowflake.ID) string {
	guild, ok := b.Client.Caches().Guilds().Get(guildID)
	if !ok {
		return "unknown"
	}
	return guild.Name
}

// Get the title of a channel
func (b *Bot) GetChannelTitle(channelID snowflake.ID) string {
	channel, ok := b.Client.Caches().Channels().Get(channelID)
	if !ok {
		return "unknown"
	}
	return channel.Name()
}

// Get a channel's threads.
func (b *Bot) GetThreadsInChannel(channelID snowflake.ID) []discord.GuildThread {
	channels := b.Client.Caches().Channels().GuildThreadsInChannel(channelID)

	// archived threads aren't cached so we need to get those and add them
	threadsObj := rest.NewThreads(b.Client.Rest())
	archivedChannels, err := threadsObj.GetPublicArchivedThreads(channelID, time.Now(), 100)
	// todo: handle the error more properly
	if err == nil {
		channels = append(channels, archivedChannels.Threads...)
	} else {
		fmt.Println(err)
	}

	return channels
}

type Messages struct {
	Messages []discord.Message
	Error    error
}

// Get the last 100 messages in a channel.
func (b *Bot) GetMessagesInChannel(channelID snowflake.ID) (messages Messages) {
	done := false
	before := snowflake.ID(0)
	for !done {
		// get the past 100 messages in a channel.
		msgs, err := b.Client.Rest().GetMessages(channelID, 100, before, 0, 0)
		if err != nil {
			messages.Error = err
			return
		}
		for _, message := range msgs {
			messages.Messages = append([]discord.Message{message}, messages.Messages...)
		}
		// If we were only able to get up to 100 messages,
		// search again but after the message we stopped at.
		if len(msgs) >= 100 {
			before = msgs[99].ID
		} else {
			done = true
		}
	}
	return
}

// Get a channel unless it's not one we should be able to view,
func (b *Bot) GetChannel(channelID snowflake.ID) (discord.Channel, error) {
	channel, ok := b.Client.Caches().Channels().Get(channelID)
	if !ok {
		return nil, fmt.Errorf("channel not found")
	}
	if channel.Type() != discord.ChannelTypeGuildForum && channel.Type() != discord.ChannelTypeGuildPublicThread {
		return nil, fmt.Errorf("channel is not a forum")
	}
	return channel, nil
}

// Count all the messages in all the threads of a channel.
func (b *Bot) PostCount(channelID snowflake.ID) string {
	threads := b.GetThreadsInChannel(channelID)
	count := 0
	for _, v := range threads {
		messages := b.GetMessagesInChannel(v.ID())
		count += len(messages.Messages)
	}
	return fmt.Sprint(count)
}

// Analyze content for mentions and emojis
func (b *Bot) FormatDiscordThings(guildID snowflake.ID, content string) template.HTML {
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

func (b *Bot) FormatUserMention(guildID snowflake.ID, userID snowflake.ID) string {
	var memberID string
	numOnlyRe.ReplaceAllString(memberID, userID.String())
	member, ok := b.Client.Caches().Members().Get(guildID, userID)
	if !ok {
		return fmt.Sprintf("<@%d>", userID)
	}
	return "@" + member.EffectiveName()
}

// Get the number of guilds we're in.
func (b *Bot) GuildNum() string {
	return strconv.Itoa(b.Client.Caches().Guilds().Len())
}
