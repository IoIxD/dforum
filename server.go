package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/state"
	"github.com/diamondburned/arikawa/v3/utils/httputil"
	"github.com/go-chi/chi/v5"
)

type server struct {
	r *chi.Mux

	discord      *state.State
	messageCache *messageCache

	sitemap        []byte
	sitemapUpdated time.Time
	sitemapMu      sync.Mutex

	buffers *sync.Pool
}

func newServer(discord *state.State, fsys fs.FS) *server {
	s := new(server)
	s.discord = discord
	s.messageCache = newMessageCache(discord.Client)
	discord.AddHandler(func(m *gateway.MessageCreateEvent) {
		s.messageCache.Set(m.Message, false)
	})
	discord.AddHandler(func(m *gateway.MessageUpdateEvent) {
		s.messageCache.Set(m.Message, true)
	})
	discord.AddHandler(func(m *gateway.MessageDeleteEvent) {
		s.messageCache.Remove(m.ChannelID, m.ID)
	})
	s.buffers = &sync.Pool{
		New: func() any {
			return new(bytes.Buffer)
		},
	}
	r := chi.NewRouter()
	s.r = r
	r.Get(`/sitemap.xml`, s.getSitemap)
	r.Get("/", s.getIndex)
	r.Route("/{guildID:\\d+}", func(r chi.Router) {
		r.Get("/", s.getGuild)
		r.Route("/{forumID:\\d+}", func(r chi.Router) {
			r.Get("/", s.getForum)
			r.Route("/{postID:\\d+}", func(r chi.Router) {
				r.Get("/", s.getPost)
			})
		})
	})
	r.Get("/privacy", s.PrivacyPage)
	r.Get("/static/*", http.FileServer(http.FS(fsys)).ServeHTTP)
	r.NotFound(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		displayErr(w, http.StatusNotFound, nil)
	}))
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.r.ServeHTTP(w, r)
}

func (s *server) executeTemplate(w http.ResponseWriter, name string, ctx any) {
	buf := s.buffers.Get().(*bytes.Buffer)
	if err := tmpl.ExecuteTemplate(buf, name, ctx); err == nil {
		io.Copy(w, buf)
	} else {
		displayErr(w, http.StatusInternalServerError, err)
	}
	buf.Reset()
	s.buffers.Put(buf)
}

func displayErr(w http.ResponseWriter, status int, err error) {
	ctx := struct {
		Error      error
		StatusText string
		StatusCode int
	}{err, http.StatusText(status), status}
	w.WriteHeader(status)
	tmpl.ExecuteTemplate(w, "error.gohtml", ctx)
}

func discordStatusIs(err error, status int) bool {
	var httperr *httputil.HTTPError
	if ok := errors.As(err, &httperr); !ok {
		return false
	}
	return httperr.Status == status
}

func (s *server) publicActiveThreads(gid discord.GuildID) ([]discord.Channel, error) {
	channels, err := s.discord.Cabinet.Channels(gid)
	if err != nil {
		return nil, err
	}
	var threads []discord.Channel
	for _, channel := range channels {
		if channel.Type != discord.GuildPublicThread {
			continue
		}
		threads = append(threads, channel)
	}
	return threads, nil
}

func (s *server) getIndex(w http.ResponseWriter, r *http.Request) {
	guilds, err := s.discord.Cabinet.Guilds()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	ctx := struct {
		GuildCount int
	}{len(guilds)}
	s.executeTemplate(w, "index.gohtml", ctx)
}

type ForumChannel struct {
	discord.Channel
	Posts             []discord.Channel
	TotalMessageCount int
	LastActive        time.Time
}

func (s *server) getGuild(w http.ResponseWriter, r *http.Request) {
	guild, ok := s.guildFromReq(w, r)
	if !ok {
		return
	}
	ctx := struct {
		Guild         *discord.Guild
		ForumChannels []ForumChannel
	}{Guild: guild}
	channels, err := s.discord.Cabinet.Channels(guild.ID)
	if err != nil {
		displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching guild channels: %s", err))
		return
	}
	for _, ch := range channels {
		fmt.Println(ch.Type)
		if ch.Type == discord.GuildForum {
			var posts []discord.Channel
			for _, t := range channels {
				if t.ParentID == ch.ID &&
					t.Type == discord.GuildPublicThread {
					posts = append(posts, t)
				}
			}
			var msgcount int
			for _, post := range posts {
				msgcount += post.MessageCount
			}
			var lastactive time.Time
			if ch.LastMessageID.IsValid() {
				lastactive = ch.LastMessageID.Time()
			}
			for _, post := range posts {
				if post.LastMessageID.Time().After(lastactive) {
					lastactive = post.LastMessageID.Time()
				}
			}
			ctx.ForumChannels = append(ctx.ForumChannels, ForumChannel{
				ch, posts, msgcount, lastactive,
			})
		}
	}
	sort.SliceStable(ctx.ForumChannels, func(i, j int) bool {
		return ctx.ForumChannels[i].LastActive.Before(ctx.ForumChannels[j].LastActive)
	})
	s.executeTemplate(w, "guild.gohtml", ctx)
}

func (s *server) getForum(w http.ResponseWriter, r *http.Request) {
	guild, ok := s.guildFromReq(w, r)
	if !ok {
		return
	}
	forum, ok := s.forumFromReq(w, r)
	if !ok {
		return
	}

	ctx := struct {
		Guild *discord.Guild
		Forum *discord.Channel
		Posts []discord.Channel
	}{guild, forum, nil}
	channels, err := s.discord.Cabinet.Channels(guild.ID)
	if err != nil {
		displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching guild threads: %w", err))
		return
	}
	for _, thread := range channels {
		if thread.ParentID == forum.ID &&
			thread.Type == discord.GuildPublicThread {
			ctx.Posts = append(ctx.Posts, thread)
		}
	}
	sort.SliceStable(ctx.Posts, func(i, j int) bool {
		if ctx.Posts[i].Flags^ctx.Posts[j].Flags&discord.PinnedThread != 0 {
			return ctx.Posts[i].Flags&discord.PinnedThread != 0
		}
		return ctx.Posts[i].LastMessageID.Time().Before(ctx.Posts[j].LastMessageID.Time())
	})
	s.executeTemplate(w, "forum.gohtml", ctx)
}

func (s *server) getPost(w http.ResponseWriter, r *http.Request) {
	guild, ok := s.guildFromReq(w, r)
	if !ok {
		return
	}
	forum, ok := s.forumFromReq(w, r)
	if !ok {
		return
	}
	post, ok := s.postFromReq(w, r)
	if !ok {
		return
	}
	ctx := struct {
		Guild         *discord.Guild
		Forum         *discord.Channel
		Post          *discord.Channel
		MessageGroups []MessageGroup
	}{guild, forum, post, nil}

	msgs, err := s.messageCache.Messages(post.ID)
	if err != nil {
		displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching post's messages: %w", err))
		return
	}
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].ID < msgs[j].ID
	})
	var msgrps []MessageGroup
	i := -1
	for _, m := range msgs {
		m.GuildID = guild.ID
		msg := s.message(m)
		if i == -1 || msgrps[i].Author.ID != m.Author.ID {
			auth := s.author(m)
			msgrps = append(msgrps, MessageGroup{auth, []Message{msg}})
			i++
		} else {
			msgrps[i].Messages = append(msgrps[i].Messages, msg)
		}
	}
	ctx.MessageGroups = msgrps
	s.executeTemplate(w, "post.gohtml", ctx)
}

func (s *server) guildFromReq(w http.ResponseWriter, r *http.Request) (*discord.Guild, bool) {
	guildIDsf, err := discord.ParseSnowflake(chi.URLParam(r, "guildID"))
	if err != nil {
		displayErr(w, http.StatusBadRequest, err)
		return nil, false
	}
	guildID := discord.GuildID(guildIDsf)
	guild, err := s.discord.Cabinet.Guild(guildID)
	if err != nil {
		if discordStatusIs(err, http.StatusNotFound) {
			displayErr(w, http.StatusNotFound, nil)
		} else {
			displayErr(w, http.StatusInternalServerError,
				fmt.Errorf("fetching guild: %w", err))
		}
		return nil, false
	}
	return guild, true
}

func (s *server) forumFromReq(w http.ResponseWriter, r *http.Request) (*discord.Channel, bool) {
	forumIDsf, err := discord.ParseSnowflake(chi.URLParam(r, "forumID"))
	if err != nil {
		displayErr(w, http.StatusBadRequest, err)
		return nil, false
	}
	forumID := discord.ChannelID(forumIDsf)
	forum, err := s.discord.Cabinet.Channel(forumID)
	if err != nil {
		if discordStatusIs(err, http.StatusNotFound) {
			displayErr(w, http.StatusNotFound, nil)
		} else {
			displayErr(w, http.StatusInternalServerError,
				fmt.Errorf("fetching forum: %w", err))
		}
		return nil, false
	}
	if forum.NSFW {
		displayErr(w, http.StatusForbidden,
			errors.New("NSFW content is not served"))
		return nil, false
	}
	return forum, true
}

func (s *server) postFromReq(w http.ResponseWriter, r *http.Request) (*discord.Channel, bool) {
	postIDsf, err := discord.ParseSnowflake(chi.URLParam(r, "postID"))
	if err != nil {
		displayErr(w, http.StatusBadRequest, err)
		return nil, false
	}
	postID := discord.ChannelID(postIDsf)
	post, err := s.discord.Cabinet.Channel(postID)
	if err != nil {
		if discordStatusIs(err, http.StatusNotFound) {
			displayErr(w, http.StatusNotFound, nil)
		} else {
			displayErr(w, http.StatusInternalServerError,
				fmt.Errorf("fetching post: %w", err))
		}
		return nil, false
	}
	return post, true
}

func (s *server) PrivacyPage(w http.ResponseWriter, r *http.Request) {
	s.executeTemplate(w, "privacy.gohtml", nil)
}
