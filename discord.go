package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"

	"github.com/IoIxD/dforum/database"
	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/state"
)

func (s *server) channels(guildID discord.GuildID) ([]discord.Channel, error) {
	s.fetchedInactiveMu.Lock()
	defer s.fetchedInactiveMu.Unlock()
	channels, err := s.discord.Channels(guildID)
	if err != nil {
		return nil, err
	}
	guild, _ := s.discord.Cabinet.Guild(guildID)
	me, _ := s.discord.Cabinet.Me()
	selfMember, err := s.discord.Member(guildID, me.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get self as member: %w", err)
	}
	for _, ch := range channels {
		if ch.Type != discord.GuildForum {
			continue
		}
		if _, ok := s.fetchedInactive[ch.ID]; ok {
			continue
		}
		perms := discord.CalcOverwrites(*guild, ch, *selfMember)
		if !perms.Has(0 |
			discord.PermissionReadMessageHistory |
			discord.PermissionViewChannel) {
			continue
		}
		var before discord.Timestamp
		for {
			threads, err := s.discord.PublicArchivedThreads(ch.ID, before, 0)
			if err != nil {
				return nil, err
			}
			for _, t := range threads.Threads {
				s.discord.Cabinet.ChannelStore.ChannelSet(&t, false)
				channels = append(channels, t)
			}
			if !threads.More {
				break
			}
			before = threads.Threads[len(threads.Threads)-1].ThreadMetadata.ArchiveTimestamp
		}
		s.fetchedInactive[ch.ID] = struct{}{}
	}
	return channels, nil
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
	st       *state.State
	db       database.Database
	channels sync.Map // discord.ChannelID -> *channel
}

// fetchCallback is a callback that is ran every time a batch of messages is
// fetched. It returns true when it should stop being called.
type fetchCallback func(msgs []discord.Message, full bool, err error) (done bool)

type channel struct {
	mut      sync.Mutex
	uptodate *bool

	fetchCallbacks chan<- fetchCallback
	fetchDone      <-chan struct{}
}

func newMessageCache(c *state.State, db database.Database) *messageCache {
	return &messageCache{
		st: c,
		db: db,
	}
}

func (c *messageCache) channel(chID discord.ChannelID) (*channel, error) {
	v, _ := c.channels.LoadOrStore(chID, &channel{})
	ch := v.(*channel)
	ch.mut.Lock()
	if ch.uptodate != nil {
		return ch, nil
	}
	upd, err := c.db.UpdatedAt(context.Background(), chID)
	if err != nil {
		ch.mut.Unlock()
		return nil, err
	}
	if upd.IsZero() {
		b := false
		ch.uptodate = &b
		return ch, nil
	}
	channel, err := c.st.Channel(chID)
	if err != nil {
		ch.mut.Unlock()
		return nil, err
	}
	if !channel.ThreadMetadata.Archived {
		b := false
		ch.uptodate = &b
		return ch, nil
	}
	b := channel.ThreadMetadata.ArchiveTimestamp.Time().Before(upd)
	ch.uptodate = &b
	return ch, nil
}

func (c *messageCache) HandleThreadUpdateEvent(ev *gateway.ThreadUpdateEvent) error {
	ch, err := c.channel(ev.ID)
	if err != nil {
		return err
	}
	if !ev.ThreadMetadata.Archived {
		ch.mut.Unlock()
		return nil
	}
	uptodate := *ch.uptodate
	ch.mut.Unlock()
	if uptodate {
		return c.db.SetUpdatedAt(context.Background(), ev.ID, ev.ThreadMetadata.ArchiveTimestamp.Time())
	}
	return nil
}

func (c *messageCache) Set(ctx context.Context, m discord.Message, update bool) error {
	ch, err := c.channel(m.ChannelID)
	if err != nil {
		return err
	}
	if *ch.uptodate {
		ch.mut.Unlock()
	} else {
		fetchdone := ch.fetchDone
		ch.mut.Unlock()
		if fetchdone != nil {
			<-fetchdone
		} else {
			return nil
		}
	}
	if update {
		return c.db.UpdateMessage(ctx, m)
	} else {
		return c.db.InsertMessage(ctx, m)
	}
}

func (c *messageCache) Remove(ctx context.Context, chid discord.ChannelID, id discord.MessageID) error {
	ch, err := c.channel(chid)
	if err != nil {
		return err
	}
	if *ch.uptodate {
		ch.mut.Unlock()
	} else {
		fetchdone := ch.fetchDone
		ch.mut.Unlock()
		if fetchdone != nil {
			<-fetchdone
		} else {
			return nil
		}
	}
	return c.db.DeleteMessage(ctx, id)
}

type result struct {
	msgs []discord.Message
	err  error
}

func (c *messageCache) MessagesAfter(ctx context.Context, chID discord.ChannelID, m discord.MessageID, limit uint) (messages []discord.Message, hasbefore, hasafter bool, err error) {
	ch, err := c.channel(chID)
	if err != nil {
		return
	}
	if *ch.uptodate {
		ch.mut.Unlock()
		messages, hasbefore, err = c.db.MessagesAfter(ctx, chID, m, limit+1)
		if err != nil {
			return
		}
		if len(messages) == int(limit)+1 {
			hasafter = true
			messages = messages[:len(messages)-1]
		}
		return
	}
	c.messages(ch, chID, func(msgs []discord.Message, full bool, e error) (done bool) {
		select {
		case <-ctx.Done():
			return true
		default:
		}
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

func (c *messageCache) MessagesBefore(ctx context.Context, chID discord.ChannelID, m discord.MessageID, limit uint) (messages []discord.Message, hasbefore, hasafter bool, err error) {
	ch, err := c.channel(chID)
	if err != nil {
		return
	}
	if *ch.uptodate {
		ch.mut.Unlock()
		messages, hasafter, err = c.db.MessagesBefore(ctx, chID, m, limit+1)
		if err != nil {
			return
		}
		if len(messages) == int(limit)+1 {
			hasbefore = true
			messages = messages[1:]
		}
		return
	}
	c.messages(ch, chID, func(msgs []discord.Message, full bool, e error) (done bool) {
		select {
		case <-ctx.Done():
			return true
		default:
		}
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

func (c *messageCache) messages(ch *channel, chid discord.ChannelID, fn fetchCallback) {
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
	fetchdone := make(chan struct{})
	callbacks <- wrapped
	ch.fetchDone = fetchdone
	ch.fetchCallbacks = callbacks
	ch.mut.Unlock()
	go func() {
		msgs, err := load(c.st.Client, chid, callbacks)
		ch.mut.Lock()
		close(fetchdone)
		err = c.db.UpdateMessages(context.Background(), chid, msgs)
		if err != nil {
			// TODO(samhza): handle this better
			log.Println("updating messages:", err)
		}
		ch.fetchCallbacks = nil
		ch.fetchDone = nil
		b := err == nil
		ch.uptodate = &b
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
				if len(msgs) == 0 || !f(msgs, false, err) {
					callbacks = append(callbacks, f)
				}
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
			if !f(msgs, len(m) < 100, nil) {
				retainedcallbacks = append(retainedcallbacks, f)
			}
		}
		if len(m) < 100 {
			break
		}
		callbacks = retainedcallbacks
		after = m[99].ID
	}
	return msgs, err
}
