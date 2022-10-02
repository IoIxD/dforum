package main

import (
	"context"
	"sync"

	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
)

// ensureArchivedThreads ensures that all archived threads in the channel are in
// the cache.
func (s *server) ensureArchivedThreads(cid discord.ChannelID) error {
	s.fetchedInactiveMu.Lock()
	defer s.fetchedInactiveMu.Unlock()
	if _, ok := s.fetchedInactive[cid]; ok {
		return nil
	}
	threads, err := s.discord.PublicArchivedThreadsBefore(cid, discord.Timestamp{}, 0)
	if err != nil {
		return err
	}
	for _, t := range threads.Threads {
		s.discord.Cabinet.ChannelStore.ChannelSet(&t, false)
	}
	s.fetchedInactive[cid] = struct{}{}
	return nil
}

// ensureMembers ensures that all message authors are in the cache.
func (s *server) ensureMembers(ctx context.Context, post discord.Channel, msgs []discord.Message) error {
	s.requestMembers.Lock()
	defer s.requestMembers.Unlock()
	if _, ok := s.membersGot[post.ID]; ok {
		return nil
	}
	missing := make(map[discord.UserID]struct{})
	for _, msg := range msgs {
		if _, err := s.discord.Cabinet.Member(post.GuildID, msg.Author.ID); err != nil {
			missing[msg.Author.ID] = struct{}{}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	missingslice := make([]discord.UserID, 0, len(missing))
	for id := range missing {
		missingslice = append(missingslice, id)
	}
	out, cancel := s.discord.ChanFor(
		func(ev interface{}) bool {
			_, ok := ev.(*gateway.GuildMembersChunkEvent)
			return ok
		})
	defer cancel()
	s.discord.Gateway().Send(ctx, &gateway.RequestGuildMembersCommand{
		GuildIDs: []discord.GuildID{post.GuildID},
		UserIDs:  missingslice,
	})
	for {
		select {
		case e := <-out:
			chunk := e.(*gateway.GuildMembersChunkEvent)
			if chunk.ChunkIndex == chunk.ChunkCount-1 {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

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
	if !ok {
		return
	}
	ch := v.(*channel)
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
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}
		ch.msgs = msgs
	}
	msgs := make([]discord.Message, len(ch.msgs))
	copy(msgs, ch.msgs)
	return msgs, nil
}
