package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/log"
)

// things that need to be replaced with html or otherwise

var replacer = strings.NewReplacer(
	"\n", "<br>",
	"&lt;", "<span><</span>",
	"&gt;", "<span>></span>",
)

// regexp checks
var mentionRe = regexp.MustCompile(`<@([0-9]*)>`)
var emojiRe = regexp.MustCompile(`<:([A-z]*?):([0-9]*)>`)
var numOnlyRe = regexp.MustCompile(`([^0-9])`)

func InitBot() *Bot {
	log.SetLevel(log.LevelDebug)
	client, err := disgo.New(LocalConfig.BotToken,
		bot.WithGatewayConfigOpts(
			gateway.WithIntents(gateway.IntentsNonPrivileged, gateway.IntentMessageContent),
		),
		bot.WithCacheConfigOpts(
			cache.WithCacheFlags(cache.FlagGuilds, cache.FlagChannels, cache.FlagMembers, cache.FlagEmojis, cache.FlagStickers),
		),
		bot.WithEventListenerFunc(func(e *events.Ready) {
			selfUser, _ := e.Client().Caches().GetSelfUser()
			fmt.Printf("Logged in as: %s\n", selfUser.Tag())
		}),
	)
	if err != nil {
		log.Fatalf("error while creating client: %s", err)
	}

	if err = client.OpenGateway(context.Background()); err != nil {
		log.Fatalf("error while connecting to discord: %s", err)
	}

	return &Bot{
		Client: client,
	}
}
