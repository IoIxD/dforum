package main

import (
	"context"
	"sort"
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
	var before discord.Timestamp
	for {
		threads, err := s.discord.PublicArchivedThreads(cid, before, 100)
		if err != nil {
			return err
		}
		for _, t := range threads.Threads {
			s.discord.Cabinet.ChannelStore.ChannelSet(&t, false)
		}
		if !threads.More {
			break
		}
		before = threads.Threads[len(threads.Threads)-1].ThreadMetadata.ArchiveTimestamp
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

// fetchCallback is a callback that is ran every time a batch of messages is
// fetched. It returns true when it should stop being called.
type fetchCallback func(msgs []discord.Message, full bool, err error) (done bool)

type channel struct {
	mut  sync.Mutex
	msgs []discord.Message

	fetchCallbacks chan<- fetchCallback
	// fetchDone is closed when the messages have been fully fetched.
	fetchDone <-chan struct{}
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

type result struct {
	msgs []discord.Message
	err  error
}

func (c *messageCache) MessagesAfter(ch discord.ChannelID, m discord.MessageID, limit uint) (messages []discord.Message, hasbefore, hasafter bool, err error) {
	c.messages(ch, func(msgs []discord.Message, full bool, e error) (done bool) {
		if e != nil {
			err = e
			return true
		}
		i := sort.Search(len(msgs), func(i int) bool {
			return msgs[i].ID >= m
		})
		if i >= len(msgs) {
			return true
		}
		if msgs[i].ID == m {
			i++
		}
		if i > 0 {
			hasbefore = true
		}
		if len(msgs)-i > int(limit) {
			messages = msgs[i : i+int(limit)]
			hasafter = true
			return true
		}
		messages = msgs[i:]
		return full
	})
	return
}

func (c *messageCache) MessagesBefore(ch discord.ChannelID, m discord.MessageID, limit uint) (messages []discord.Message, hasbefore, hasafter bool, err error) {
	c.messages(ch, func(msgs []discord.Message, full bool, e error) (done bool) {
		if e != nil {
			err = e
			return true
		}
		i := sort.Search(len(msgs), func(i int) bool {
			return msgs[i].ID >= m
		})
		if i == 0 {
			return true
		}
		if i == len(msgs) && !full {
			return false
		}
		if i < len(msgs) {
			hasafter = true
		}
		if uint(i) > limit {
			messages = make([]discord.Message, limit)
			copy(messages, msgs[i-int(limit):i])
			hasbefore = true
			return true
		}
		messages = make([]discord.Message, i)
		copy(messages, msgs[:i])
		return true
	})
	return
}

func (c *messageCache) messages(id discord.ChannelID, fn fetchCallback) {
	v, _ := c.channels.LoadOrStore(id, &channel{})
	ch := v.(*channel)
	ch.mut.Lock()
	if ch.msgs != nil {
		fn(ch.msgs, true, nil)
		ch.mut.Unlock()
		return
	}
	done := make(chan struct{})
	wrapped := func(msgs []discord.Message, good bool, err error) bool {
		found := fn(msgs, good, err)
		if found || good {
			close(done)
			return true
		}
		return false
	}
	if ch.fetchCallbacks != nil {
		ch.fetchCallbacks <- wrapped
		ch.mut.Unlock()
		<-done
		return
	}
	callbacks := make(chan fetchCallback, 1)
	callbacks <- wrapped
	ch.fetchCallbacks = callbacks
	ch.mut.Unlock()
	go func() {
		msgs, err := load(c.Client, id, callbacks)
		ch.mut.Lock()
		ch.fetchCallbacks = nil
		ch.msgs = msgs
	Outer:
		for {
			select {
			case fn := <-callbacks:
				fn(msgs, true, err)
			default:
				break Outer
			}
		}
		ch.mut.Unlock()
	}()
	<-done
	return
}

func load(client *api.Client, chanID discord.ChannelID, callbackchan <-chan fetchCallback) ([]discord.Message, error) {
	var after discord.MessageID
	var err error
	var msgs []discord.Message
	var callbacks []fetchCallback
	for {
		var m []discord.Message
		done := make(chan struct{})
		go func() {
			m, err = client.MessagesAfter(chanID, after, 100)
			done <- struct{}{}
		}()
	Outer:
		for {
			select {
			case f := <-callbackchan:
				callbacks = append(callbacks, f)
			case <-done:
				break Outer
			}
		}
		if err != nil {
			break
		}
		for i, j := 0, len(m)-1; i < j; i, j = i+1, j-1 {
			m[i], m[j] = m[j], m[i]
		}
		msgs = append(msgs, m...)
		retainedcallbacks := callbacks[:0]
		for _, f := range callbacks {
			if !f(msgs, false, nil) {
				retainedcallbacks = append(retainedcallbacks, f)
			}
		}
		callbacks = retainedcallbacks
		if len(m) < 100 {
			break
		}
		after = m[99].ID
	}
	return msgs, err
}
