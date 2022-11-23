package database

import (
	"context"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"
)

type Database interface {
	Close() error

	SetUpdatedAt(ctx context.Context, post discord.ChannelID, t time.Time) error
	UpdatedAt(ctx context.Context, post discord.ChannelID) (time.Time, error)
	UpdateMessages(ctx context.Context, post discord.ChannelID, msgs []discord.Message) error
	InsertMessage(ctx context.Context, msg discord.Message) error
	UpdateMessage(ctx context.Context, msg discord.Message) error
	DeleteMessage(ctx context.Context, msg discord.MessageID) error
	MessagesAfter(ctx context.Context, post discord.ChannelID, after discord.MessageID, limit uint) ([]discord.Message, bool, error)
	MessagesBefore(ctx context.Context, post discord.ChannelID, before discord.MessageID, limit uint) ([]discord.Message, bool, error)
}
