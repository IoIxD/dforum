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
	"github.com/diamondburned/arikawa/v3/state"
	"github.com/diamondburned/arikawa/v3/utils/httputil"
	"github.com/go-chi/chi/v5"
)

type server struct {
	r *chi.Mux

	discord *state.State

	sitemap        []byte
	sitemapUpdated time.Time
	sitemapMu      sync.Mutex

	buffers *sync.Pool
}

func newServer(discord *state.State, fsys fs.FS) *server {
	s := new(server)
	s.discord = discord
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
	Posts []discord.Channel
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
	channels, err := s.discord.Channels(guild.ID)
	if err != nil {
		displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching guild channels: %s", err))
		return
	}
	threads, err := s.discord.ActiveThreads(guild.ID)
	if err != nil {
		displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching guild active threads: %s", err))
		return
	}
	for _, ch := range channels {
		if ch.Type == discord.GuildForum {
			var posts []discord.Channel
			for _, t := range threads.Threads {
				if t.ParentID == ch.ID {
					posts = append(posts, t)
				}
			}
			ctx.ForumChannels = append(ctx.ForumChannels, ForumChannel{
				ch, posts,
			})
		}
	}
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

	if forum.NSFW {
		displayErr(w, http.StatusForbidden,
			fmt.Errorf("nsfw content is not served"))
		return
	}

	ctx := struct {
		Guild *discord.Guild
		Forum *discord.Channel
		Posts []discord.Channel
	}{guild, forum, nil}
	guildThreads, err := s.discord.ActiveThreads(guild.ID)
	if err != nil {
		displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching guild threads: %w", err))
	}
	for _, t := range guildThreads.Threads {
		if t.ParentID == forum.ID {
			ctx.Posts = append(ctx.Posts, t)
		}
	}
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
	if forum.NSFW {
		displayErr(w, http.StatusForbidden,
			fmt.Errorf("nsfw content is not served"))
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
		MessageGroups [][]Message
	}{guild, forum, post, nil}

	msgs, err := s.discord.Client.Messages(post.ID, 0)
	if err != nil {
		displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching post's messages: %w", err))
		return
	}
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].ID < msgs[j].ID
	})
	var msgrps [][]Message
	i := -1
	for _, m := range msgs {
		msg := s.message(m)
		if i == -1 || msgrps[i][0].Author.ID != m.Author.ID {
			msgrps = append(msgrps, []Message{msg})
			i++
		} else {
			msgrps[i] = append(msgrps[i], msg)
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
	guild, err := s.discord.Guild(guildID)
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
	forum, err := s.discord.Channel(forumID)
	if err != nil {
		if discordStatusIs(err, http.StatusNotFound) {
			displayErr(w, http.StatusNotFound, nil)
		} else {
			displayErr(w, http.StatusInternalServerError,
				fmt.Errorf("fetching forum: %w", err))
		}
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
	post, err := s.discord.Channel(postID)
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
