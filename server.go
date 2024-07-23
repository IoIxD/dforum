package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/IoIxD/dforum/database"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/state"
	"github.com/diamondburned/arikawa/v3/utils/httputil"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/exp/slices"
)

type server struct {
	r *chi.Mux

	discord      *state.State
	messageCache *messageCache

	fetchedInactiveMu sync.Mutex
	fetchedInactive   map[discord.ChannelID]struct{}

	requestMembers sync.Mutex
	membersGot     map[discord.ChannelID]struct{}

	sitemapMu     sync.Mutex
	updateSitemap chan struct{}

	// configuration options
	URL               string
	ServiceName       string
	ServerHostedIn    string
	SitemapDir        string
	executeTemplateFn ExecuteTemplateFunc

	buffers *sync.Pool

	optionsRegex *regexp.Regexp
}

type ExecuteTemplateFunc func(w io.Writer, name string, data interface{}) error

func newServer(st *state.State, fsys fs.FS, db database.Database, config config) (*server, error) {
	optionsRegex, err := regexp.Compile(`<\?dforum (.*?)\?>`)
	if err != nil {
		return nil, err
	}
	srv := &server{
		fetchedInactive: make(map[discord.ChannelID]struct{}),
		discord:         st,
		messageCache:    newMessageCache(st, db),
		buffers:         &sync.Pool{New: func() interface{} { return new(bytes.Buffer) }},
		URL:             config.SiteURL,
		ServiceName:     config.ServiceName,
		ServerHostedIn:  config.ServerHostedIn,
		optionsRegex:    optionsRegex,
		SitemapDir:      config.SitemapDir,
	}
	st.AddHandler(func(m *gateway.MessageCreateEvent) {
		srv.messageCache.Set(context.Background(), m.Message, false)
	})
	st.AddHandler(func(m *gateway.MessageUpdateEvent) {
		srv.messageCache.Set(context.Background(), m.Message, true)
	})
	st.AddHandler(func(m *gateway.MessageDeleteEvent) {
		srv.messageCache.Remove(context.Background(), m.ChannelID, m.ID)
	})
	st.AddHandler(func(m *gateway.ThreadUpdateEvent) {
		srv.messageCache.HandleThreadUpdateEvent(m)
	})
	r := chi.NewRouter()
	srv.r = r
	srv.updateSitemap = make(chan struct{}, 1)
	r.Use(middleware.Logger)
	getHead(r, `/sitemap/*`, srv.getSitemap)
	getHead(r, `/sitemap.xml`, srv.getSitemap)
	getHead(r, "/", srv.getIndex)
	r.Route("/{guildID:\\d+}", func(r chi.Router) {
		getHead(r, "/", srv.getGuild)
		r.Route("/{forumID:\\d+}", func(r chi.Router) {
			getHead(r, "/", srv.getForum)
			getHead(r, "/search", srv.searchForum)
			r.Route("/page/{page:\\d+}", func(r chi.Router) {
				getHead(r, "/", srv.getForum)
				getHead(r, "/search", srv.searchForum)
			})
			r.Route("/{postID:\\d+}", func(r chi.Router) {
				getHead(r, "/", srv.getPost)
			})
		})
	})

	getHead(r, "/privacy", srv.PrivacyPage)
	getHead(r, "/tos", srv.TOSPage)
	getHead(r, "/static/*", http.FileServer(http.FS(fsys)).ServeHTTP)
	r.NotFound(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		srv.displayErr(w, http.StatusNotFound, nil)
	}))
	return srv, nil
}

func (s *server) UpdateSitemap() {
	first := true
	ticker := time.NewTicker(6 * time.Hour)
	for {
		index := filepath.Join(s.SitemapDir, "sitemap.xml")
		stat, err := os.Stat(index)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Println("Error occured while reading sitemap last modified time:", err)
			continue
		}
		if err == nil && time.Since(stat.ModTime()) < 6*time.Hour {
			continue
		} else {
			if first {
				log.Println("Waiting 60 seconds before generating sitemap.")
				time.Sleep(60 * time.Second)
				first = false
			}
			err = s.writeSitemap()
			if err != nil {
				log.Println("Error occured while writing sitemap:", err)
			}
		}
		select {
		case <-ticker.C:
		case <-s.updateSitemap:
		}
	}
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
	if err := s.executeTemplateFn(buf, name, ctx); err == nil {
		checksum := crc32.ChecksumIEEE(buf.Bytes())
		w.Header().Set("ETag", fmt.Sprintf("\"%x\"", checksum))
		rdr := bytes.NewReader(buf.Bytes())
		http.ServeContent(w, r, name, time.Time{}, rdr)
	} else {
		s.displayErr(w, http.StatusInternalServerError, err)
	}
	buf.Reset()
	s.buffers.Put(buf)
}

func (s *server) displayErr(w http.ResponseWriter, status int, err error) {
	ctx := struct {
		Error      error
		StatusText string
		StatusCode int
	}{err, http.StatusText(status), status}
	w.WriteHeader(status)
	s.executeTemplateFn(w, "error.gohtml", ctx)
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
		parent, err := s.channel(channel.ParentID)
		if err != nil {
			return nil, err
		}
		if parent.Type != discord.GuildForum {
			continue
		}
		threads = append(threads, channel)
	}
	return threads, nil
}

func filter(ss []Post, test func(Post) bool) (ret []Post) {
	for _, s := range ss {
		if test(s) {
			ret = append(ret, s)
		}
	}
	return
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

	channels, err := s.channels(guild.ID)
	if err != nil {
		s.displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching guild channels: %s", err))
		return
	}
	me, _ := s.discord.Cabinet.Me()
	selfMember, err := s.discord.Member(guild.ID, me.ID)
	if err != nil {
		s.displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("error fetching self as member: %s", err))
		return
	}
	for _, forum := range channels {
		if forum.Type != discord.GuildForum {
			continue
		}
		perms := discord.CalcOverwrites(*guild, forum, *selfMember)
		if !perms.Has(0 |
			discord.PermissionReadMessageHistory |
			discord.PermissionViewChannel) {
			continue
		}
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

func (s *server) searchGuild(w http.ResponseWriter, r *http.Request) {
	s.executeTemplate(w, r, "searchguild.gohtml", nil)
}

func (s *server) searchForum(w http.ResponseWriter, r *http.Request) {
	guild, ok := s.guildFromReq(w, r)
	if !ok {
		return
	}
	forum, ok := s.forumFromReq(w, r)
	if !ok {
		return
	}

	query := r.URL.Query().Get("q")

	if query == "" {
		s.getForum(w, r)
		return
	}

	ctx := struct {
		Guild       *discord.Guild
		Forum       *discord.Channel
		Posts       []Post
		Prev        int
		Next        int
		URL         string
		Query       string
		AppendedStr string
	}{Guild: guild,
		Forum:       forum,
		URL:         s.URL,
		Query:       query,
		AppendedStr: "/search?q=" + query,
	}
	channels, err := s.channels(guild.ID)
	if err != nil {
		s.displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching guild threads: %w", err))
		return
	}
	arr := strings.Split(query, " ")

	if query == "" {
		// Show blank page.
		s.executeTemplate(w, r, "searchforum.gohtml", ctx)
		return
	}
	var posts []Post
	titles := []string{}
	for _, thread := range channels {
		if thread.ParentID != forum.ID ||
			thread.Type != discord.GuildPublicThread {
			continue
		}
		post := Post{Channel: thread}
		for _, tag := range thread.AppliedTags {
			for _, availtag := range forum.AvailableTags {
				if availtag.ID == tag {
					post.Tags = append(post.Tags, availtag)
				}
			}
		}
		if slices.Contains(titles, post.Channel.Name) {
			continue
		}
		if strings.Contains(strings.ToLower(post.Channel.Name), query) {
			posts = append(posts, post)
			titles = append(titles, post.Channel.Name)
		} else {
			for _, str := range arr {
				if len(str) <= 1 {
					continue
				}
				if strings.Contains(strings.ToLower(post.Channel.Name), strings.ToLower(str)) {
					posts = append(posts, post)
					titles = append(titles, post.Channel.Name)
				}
			}
		}
	}
	sort.SliceStable(posts, func(i, j int) bool {
		return strings.Contains(strings.ToLower(posts[i].Channel.Name), strings.ToLower(query))
	})
	page, err := strconv.Atoi(chi.URLParam(r, "page"))
	if err != nil || page < 1 {
		page = 1
	}
	if page > 1 {
		ctx.Prev = page - 1
	}
	if len(posts) > page*25 {
		ctx.Next = page + 1
		posts = posts[(page-1)*25 : page*25]
	} else if len(posts) >= (page-1)*25 {
		posts = posts[(page-1)*25:]
	} else {
		posts = nil
	}
	ctx.Posts = posts
	s.executeTemplate(w, r, "searchforum.gohtml", ctx)
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

	ctx := struct {
		Guild       *discord.Guild
		Forum       *discord.Channel
		Posts       []Post
		Prev        int
		Next        int
		URL         string
		Query       string
		AppendedStr string
	}{Guild: guild,
		Forum: forum,
		URL:   s.URL}
	channels, err := s.channels(guild.ID)
	if err != nil {
		s.displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching guild threads: %w", err))
		return
	}
	var posts []Post
	for _, thread := range channels {

		if thread.ParentID != forum.ID ||
			thread.Type != discord.GuildPublicThread {
			continue
		}

		parent, err := s.channel(thread.ParentID)
		if err != nil {
			s.displayErr(w, http.StatusInternalServerError,
				fmt.Errorf("fetching parent channel's type: %w", err))
		}
		if parent == nil {
			continue
		}
		if parent.Type != discord.GuildForum {
			continue
		}
		post := Post{Channel: thread}
		for _, tag := range thread.AppliedTags {
			for _, availtag := range forum.AvailableTags {
				if availtag.ID == tag {
					post.Tags = append(post.Tags, availtag)
				}
			}
		}
		posts = append(posts, post)
	}
	sort.SliceStable(posts, func(i, j int) bool {
		if posts[i].Flags^posts[j].Flags&discord.PinnedThread != 0 {
			return posts[i].Flags&discord.PinnedThread != 0
		}
		return posts[i].LastMessageID.Time().After(posts[j].LastMessageID.Time())
	})
	page, err := strconv.Atoi(chi.URLParam(r, "page"))
	if err != nil || page < 1 {
		page = 1
	}
	if page > 1 {
		ctx.Prev = page - 1
	}
	if len(posts) > page*25 {
		ctx.Next = page + 1
		posts = posts[(page-1)*25 : page*25]
	} else if len(posts) >= (page-1)*25 {
		posts = posts[(page-1)*25:]
	} else {
		posts = nil
	}
	ctx.Posts = posts
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

	if forum.Type != discord.GuildForum {
		s.displayErr(w, http.StatusNotFound, fmt.Errorf("threads cannot be viewed unless they are in a forum channel"))
		return
	}
	ctx := struct {
		Guild         *discord.Guild
		Forum         *discord.Channel
		Post          *discord.Channel
		Prev          discord.MessageID
		Next          discord.MessageID
		MessageGroups []MessageGroup
		URL           string
	}{Guild: guild,
		Forum: forum,
		Post:  post,
		URL:   s.URL}

	var curstr string
	asc := true
	if after := r.URL.Query().Get("after"); after != "" {
		curstr = after
	} else if before := r.URL.Query().Get("before"); before != "" {
		asc = false
		curstr = before
	}
	var cur discord.MessageID
	if curstr != "" {
		sf, err := discord.ParseSnowflake(curstr)
		if err != nil {
			s.displayErr(w, http.StatusBadRequest,
				fmt.Errorf("invalid snowflake: %w", err))
			return
		}
		cur = discord.MessageID(sf)
	}
	var msgs []discord.Message
	var hasbefore, hasafter bool
	var err error
	if asc {
		msgs, hasbefore, hasafter, err = s.messageCache.MessagesAfter(r.Context(), post.ID, cur, 25)
	} else {
		msgs, hasbefore, hasafter, err = s.messageCache.MessagesBefore(r.Context(), post.ID, cur, 25)
	}
	if hasafter && len(msgs) > 0 {
		ctx.Next = msgs[len(msgs)-1].ID
	}
	if hasbefore && len(msgs) != 0 {
		ctx.Prev = msgs[0].ID
	}
	if err != nil {
		s.displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching post's messages: %w", err))
		return
	}
	err = s.ensureMembers(r.Context(), *post, msgs)
	if err != nil {
		s.displayErr(w, http.StatusInternalServerError,
			fmt.Errorf("fetching post's members: %w", err))
		return
	}

	topic := forum.Topic
	restrictRole := 0

	if strings.Contains(topic, "<?dforum ") {
		sections := s.optionsRegex.FindStringSubmatch(topic)
		for _, section := range sections[1:] {
			if err != nil {
				s.displayErr(w, http.StatusInternalServerError,
					fmt.Errorf("fetching forum's topic: %w", err))
				return
			}
			options := strings.Split(section, ",")
			for _, option := range options {
				parts := strings.Split(option, "=")
				if len(parts) < 2 {
					continue
				}
				key := parts[0]
				value := parts[1]
				switch key {
				case "consentrole":
					restrictRole, err = strconv.Atoi(value)
					if err != nil {
						s.displayErr(w, http.StatusInternalServerError,
							fmt.Errorf("error parsing the ID for the server's consent role: %w", err))
						return
					}
				}
			}
		}
	}

	var msgrps []MessageGroup
	i := -1
	for _, m := range msgs {
		m.GuildID = guild.ID
		msg := s.message(m)
		if i == -1 || msgrps[i].Author.ID != m.Author.ID {
			auth := s.author(m)
			if restrictRole != 0 {
				goodToGo := false
				for _, rl := range auth.OtherRoles {
					if int(rl.ID) == restrictRole {
						goodToGo = true
						break
					}
				}
				if !goodToGo {
					s.displayErr(w, http.StatusForbidden,
						errors.New("one or more users in this post did not consent to their post being shown"))
					return
				}
			}

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
		s.displayErr(w, http.StatusBadRequest, err)
		return nil, false
	}
	guildID := discord.GuildID(guildIDsf)
	guild, err := s.discord.Cabinet.Guild(guildID)
	if err != nil {
		if discordStatusIs(err, http.StatusNotFound) {
			s.displayErr(w, http.StatusNotFound, nil)
		} else {
			s.displayErr(w, http.StatusInternalServerError,
				fmt.Errorf("fetching guild: %w", err))
		}
		return nil, false
	}
	return guild, true
}

func (s *server) forumFromReq(w http.ResponseWriter, r *http.Request) (*discord.Channel, bool) {
	forumIDsf, err := discord.ParseSnowflake(chi.URLParam(r, "forumID"))
	if err != nil {
		s.displayErr(w, http.StatusBadRequest, err)
		return nil, false
	}
	forumID := discord.ChannelID(forumIDsf)
	forum, err := s.channel(forumID)
	if err != nil {
		if discordStatusIs(err, http.StatusNotFound) {
			s.displayErr(w, http.StatusNotFound, nil)
		} else {
			s.displayErr(w, http.StatusInternalServerError,
				fmt.Errorf("fetching forum: %w", err))
		}
		return nil, false
	}

	if forum.NSFW {
		s.displayErr(w, http.StatusForbidden,
			errors.New("NSFW content is not served"))
		return nil, false
	}
	return forum, true
}

func (s *server) postFromReq(w http.ResponseWriter, r *http.Request) (*discord.Channel, bool) {
	postIDsf, err := discord.ParseSnowflake(chi.URLParam(r, "postID"))
	if err != nil {
		s.displayErr(w, http.StatusBadRequest, err)
		return nil, false
	}
	postID := discord.ChannelID(postIDsf)
	post, err := s.discord.Channel(postID)
	if err != nil {
		if discordStatusIs(err, http.StatusNotFound) {
			s.displayErr(w, http.StatusNotFound, nil)
		} else {
			s.displayErr(w, http.StatusInternalServerError,
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
