package main

import (
	"sync"

	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/discord"
)

type messageCache struct {
	*api.Client
	channels sync.Map // discord.ChannelID -> *channel
}
type channel struct {
	mut  sync.Mutex
	msgs []discord.Message
}

func newMessageCache(c *api.Client) *messageCache {
	return &messageCache{
		Client: c,
	}
}

func (c *messageCache) Set(m discord.Message, update bool) {
	v, ok := c.channels.Load(m.ChannelID)
	if !ok {
		return
	}
	ch := v.(*channel)
	ch.mut.Lock()
	defer ch.mut.Unlock()
	if ch.msgs == nil {
		return
	}
	if update {
		for i := len(ch.msgs) - 1; i >= 0; i-- {
			if ch.msgs[i].ID == m.ID {
				ch.msgs[i] = m
				return
			}
		}
	}
	ch.msgs = append(ch.msgs, m)
}

func (c *messageCache) Remove(chid discord.ChannelID, id discord.MessageID) {
	v, ok := c.channels.Load(chid)
	ch := v.(*channel)
	if !ok {
		return
	}
	ch.mut.Lock()
	defer ch.mut.Unlock()
	for i, msg := range ch.msgs {
		if msg.ID == id {
			ch.msgs = append(ch.msgs[:i], ch.msgs[i+1:]...)
			return
		}
	}
}

func (c *messageCache) Messages(id discord.ChannelID) ([]discord.Message, error) {
	v, _ := c.channels.LoadOrStore(id, &channel{})
	ch := v.(*channel)
	ch.mut.Lock()
	defer ch.mut.Unlock()
	if ch.msgs == nil {
		msgs, err := c.Client.Messages(id, 0)
		if err != nil {
			return nil, err
		}
		ch.msgs = msgs
	}
	msgs := make([]discord.Message, len(ch.msgs))
	copy(msgs, ch.msgs)
	return msgs, nil
}
