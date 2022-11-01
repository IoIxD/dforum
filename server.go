package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/state"
	"github.com/diamondburned/arikawa/v3/utils/httputil"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type server struct {
	r *chi.Mux

	discord      *state.State
	messageCache *messageCache

	fetchedInactiveMu sync.Mutex
	fetchedInactive   map[discord.ChannelID]struct{}

	requestMembers sync.Mutex
	membersGot     map[discord.ChannelID]struct{}

	sitemap        []byte
	sitemapUpdated time.Time
	sitemapMu      sync.Mutex

	// configuration options
	URL            string
	ServiceName    string
	ServerHostedIn string

	buffers *sync.Pool
}

func newServer(st *state.State, fsys fs.FS, config config) (*server, error) {
	srv := &server{
		fetchedInactive: make(map[discord.ChannelID]struct{}),
		discord:         st,
		messageCache:    newMessageCache(st.Client),
		buffers:         &sync.Pool{New: func() interface{} { return new(bytes.Buffer) }},
		URL:             config.SiteURL,
		ServiceName:     config.ServiceName,
		ServerHostedIn:  config.ServerHostedIn,
	}
	st.AddHandler(func(m *gateway.MessageCreateEvent) {
		srv.messageCache.Set(m.Message, false)
	})
	st.AddHandler(func(m *gateway.MessageUpdateEvent) {
		srv.messageCache.Set(m.Message, true)
	})
	st.AddHandler(func(m *gateway.MessageDeleteEvent) {
		srv.messageCache.Remove(m.ChannelID, m.ID)
	})
	r := chi.NewRouter()
	srv.r = r
	r.Use(middleware.Logger)
	getHead(r, `/sitemap.xml`, srv.getSitemap)
	getHead(r, "/", srv.getIndex)
	r.Route("/{guildID:\\d+}", func(r chi.Router) {
		getHead(r, "/", srv.getGuild)
		r.Route("/{forumID:\\d+}", func(r chi.Router) {
			getHead(r, "/", srv.getForum)
			r.Route("/{postID:\\d+}", func(r chi.Router) {
				getHead(r, "/", srv.getPost)
			})
		})
	})
	getHead(r, "/privacy", srv.PrivacyPage)
	getHead(r, "/tos", srv.TOSPage)
	getHead(r, "/static/*", http.FileServer(http.FS(fsys)).ServeHTTP)
	r.NotFound(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		displayErr(w, http.StatusNotFound, nil)
	}))
	return srv, nil
}

func getHead(r chi.Router, path string, handler http.HandlerFunc) {
	r.Get(path, handler)
	r.Head(path, handler)
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.r.ServeHTTP(w, r)
}

func (s *server) executeTemplate(w http.ResponseWriter, r *http.Request,
	name string, ctx any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf := s.buffers.Get().(*bytes.Buffer)
	if err := tmpl.ExecuteTemplate(buf, name, ctx); err == nil {
		checksum := crc32.ChecksumIEEE(buf.Bytes())
		w.Header().Set("ETag", fmt.Sprintf("\"%x\"", checksum))
		rdr := bytes.NewReader(buf.Bytes())
		http.ServeContent(w, r, name, time.Time{}, rdr)
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
		URL        string
	}{len(guilds), s.URL}
	s.executeTemplate(w, r, "index.gohtml", ctx)
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
		URL           string
	}{Guild: guild, URL: s.URL}
	channels, err := s.discord.Cabinet.Channels(guild.ID)
	if err != nil {
		displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching guild channels: %s", err))
		return
	}
	var forums []discord.Channel
	for _, channel := range channels {
		if channel.Type != discord.GuildForum {
			continue
		}
		me, _ := s.discord.Cabinet.Me()
		perms, err := s.discord.Permissions(channel.ID, me.ID)
		if err != nil {
			displayErr(w, http.StatusInternalServerError,
				fmt.Errorf("fetching channel permissions: %s", err))
			return
		}
		if !perms.Has(0 |
			discord.PermissionReadMessageHistory |
			discord.PermissionViewChannel) {
			continue
		}
		err = s.ensureArchivedThreads(channel.ID)
		if err != nil {
			displayErr(w, http.StatusInternalServerError,
				fmt.Errorf("fetching archived threads: %s", err))
			return
		}
		forums = append(forums, channel)
	}
	channels, err = s.discord.Cabinet.Channels(guild.ID)
	if err != nil {
		displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching guild channels: %s", err))
		return
	}
	for _, forum := range forums {
		var posts []discord.Channel
		for _, t := range channels {
			if t.ParentID == forum.ID &&
				t.Type == discord.GuildPublicThread {
				posts = append(posts, t)
			}
		}
		var msgcount int
		for _, post := range posts {
			msgcount += post.MessageCount
		}
		var lastactive time.Time
		if forum.LastMessageID.IsValid() {
			lastactive = forum.LastMessageID.Time()
		}
		for _, post := range posts {
			if post.LastMessageID.Time().After(lastactive) {
				lastactive = post.LastMessageID.Time()
			}
		}
		ctx.ForumChannels = append(ctx.ForumChannels, ForumChannel{
			forum, posts, msgcount, lastactive,
		})
	}
	sort.SliceStable(ctx.ForumChannels, func(i, j int) bool {
		return ctx.ForumChannels[i].LastActive.After(ctx.ForumChannels[j].LastActive)
	})
	s.executeTemplate(w, r, "guild.gohtml", ctx)
}

type Post struct {
	discord.Channel
	Tags []discord.Tag
}

func (p Post) IsPinned() bool {
	return p.Channel.Flags&discord.PinnedThread != 0
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
	err := s.ensureArchivedThreads(forum.ID)
	if err != nil {
		displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching archived threads: %s", err))
		return
	}

	// query parameters
	tagFilterString := r.URL.Query().Get("tag-filter")
	afterString := r.URL.Query().Get("after")
	var tagFilter int
	var after int
	if tagFilterString != "" {
		tagFilter, err = strconv.Atoi(tagFilterString)
		if err != nil {
			displayErr(w, http.StatusInternalServerError,
				fmt.Errorf("parsing tag filter: %s", err))
			return
		}
	} else {
		tagFilter = -1
	}
	if afterString != "" {
		after, err = strconv.Atoi(afterString)
		if err != nil {
			displayErr(w, http.StatusInternalServerError,
				fmt.Errorf("parsing after number: %s", err))
			return
		}
	} else {
		after = 0
	}

	// limit for pages that show up.
	const limit = 25

	ctx := struct {
		Guild     *discord.Guild
		Forum     *discord.Channel
		Posts     []Post
		URL       string
		TagFilter int
		ShowPrev  bool
		PrevPage  int
		ShowNext  bool
		NextPage  int
	}{Guild: guild, Forum: forum, Posts: nil, URL: s.URL,
		TagFilter: tagFilter}
	channels, err := s.discord.Cabinet.Channels(guild.ID)
	if err != nil {
		displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching guild threads: %w", err))
		return
	}
	// due to the way discord handles threads in forums (they're basically more
	// channels), we need to go through the entire server and filter by
	// parent channel.
	// trying to limit how many results within this for loop leads to
	// unstable results.
	for _, thread := range channels {
		if thread.ParentID != forum.ID ||
			thread.Type != discord.GuildPublicThread {
			continue
		}
		show := true
		if tagFilter != -1 {
			show = false
		}
		post := Post{Channel: thread}
		for _, tag := range thread.AppliedTags {
			for _, availtag := range forum.AvailableTags {
				if availtag.ID == tag {
					post.Tags = append(post.Tags, availtag)
				}
			}
			if tag == discord.TagID(tagFilter) {
				show = true
			}
		}
		if show {
			ctx.Posts = append(ctx.Posts, post)
		}
	}

	sort.SliceStable(ctx.Posts, func(i, j int) bool {
		if ctx.Posts[i].Flags^ctx.Posts[j].Flags&discord.PinnedThread != 0 {
			return ctx.Posts[i].Flags&discord.PinnedThread != 0
		}
		return ctx.Posts[i].LastMessageID.Time().After(ctx.Posts[j].LastMessageID.Time())
	})

	// so limit the posts right here instead.
	postsFiltered := make([]Post, 0)
	postNum := 0
	show := false
	lastIgnoredPost := 0
	if after == 0 {
		show = true
	}
	for i, post := range ctx.Posts {
		if int(post.ID) == after {
			show = true
		}
		if show {
			if postNum >= limit {
				ctx.NextPage = int(post.ID)
				ctx.ShowNext = true
				break
			}
			postsFiltered = append(postsFiltered, post)
			postNum++
		} else {
			lastIgnoredPost = i + 1
			continue
		}
	}
	// if there's a post behind us, have a "previous" button for that
	if lastIgnoredPost-limit >= 0 {
		post := ctx.Posts[lastIgnoredPost-limit]
		ctx.ShowPrev = true
		ctx.PrevPage = int(post.ID)
	}
	ctx.Posts = postsFiltered

	s.executeTemplate(w, r, "forum.gohtml", ctx)
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
		URL           string
	}{guild, forum, post, nil, s.URL}

	msgs, err := s.messageCache.Messages(post.ID)
	if err != nil {
		displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching post's messages: %w", err))
		return
	}
	timeout, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	err = s.ensureMembers(timeout, *post, msgs)
	cancel()
	if err != nil {
		displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching post's members: %w", err))
		return
	}

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
	s.executeTemplate(w, r, "post.gohtml", ctx)
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
	s.executeTemplate(w, r, "privacy.gohtml", nil)
}

func (s *server) TOSPage(w http.ResponseWriter, r *http.Request) {
	ctx := struct {
		ServiceName    string
		ServerHostedIn string
	}{s.ServiceName, s.ServerHostedIn}
	s.executeTemplate(w, r, "tos.gohtml", ctx)
}
