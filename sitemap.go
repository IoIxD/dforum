package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"
)

type URL struct {
	XMLName   string  `xml:"url"`
	Location  string  `xml:"loc"`
	LastMod   string  `xml:"lastmod,omitempty"`
	Frequency string  `xml:"changefreq,omitempty"`
	Priority  float32 `xml:"priority,omitempty"`
}

func (s *server) getSitemap(w http.ResponseWriter, r *http.Request) {
	s.sitemapMu.Lock()
	var sitemap []byte
	var modtime time.Time
	if s.sitemap == nil || time.Since(s.sitemapUpdated) > 6*time.Hour {
		buf := s.buffers.Get().(*bytes.Buffer)
		err := s.writeSitemap(buf)
		if err != nil {
			s.sitemapMu.Unlock()
			buf.Reset()
			s.buffers.Put(buf)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sitemap = make([]byte, buf.Len())
		copy(sitemap, buf.Bytes())
		s.sitemap = sitemap
		s.sitemapUpdated = time.Now()
		modtime = s.sitemapUpdated
		s.sitemapMu.Unlock()
		buf.Reset()
		s.buffers.Put(buf)
	} else {
		sitemap = s.sitemap
		modtime = s.sitemapUpdated
		s.sitemapMu.Unlock()
	}
	rdr := bytes.NewReader(sitemap)
	http.ServeContent(w, r, "sitemap.xml", modtime, rdr)
}

var XMLURLSetStart = xml.StartElement{
	Name: xml.Name{Local: "urlset"},
	Attr: []xml.Attr{
		{Name: xml.Name{Local: "xmlns"}, Value: "http://www.sitemaps.org/schemas/sitemap/0.9"},
	},
}

var XMLURLSetEnd = XMLURLSetStart.End()

type URLMap struct {
	urls map[int]URL
	sync.RWMutex
}

func NewURLMap() URLMap {
	return URLMap{
		urls: make(map[int]URL),
	}
}

func (u *URLMap) Push(url URL) {
	u.Lock()
	defer u.Unlock()
	u.urls[len(u.urls)] = url
}

func (s *server) writeSitemap(w io.Writer) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	var err error
	if err = enc.EncodeToken(XMLURLSetStart); err != nil {
		return err
	}
	guilds, _ := s.discord.Cabinet.Guilds()
	// we abuse goroutines to get around the fact that we're about to do some expensive work;
	// we can just change the solution entirely but this would get messy when we inevitably switch
	// to a sql database for caching.
	urls := NewURLMap()
	var wg1 sync.WaitGroup
	wg1.Add(len(guilds))
	errCh1 := make(chan error)
	waitCh1 := make(chan bool)
	// level 1 wrapper
	go func() {
		for _, guild := range guilds {
			// level 1
			go func(guild discord.Guild) {
				defer wg1.Done()
				urls.Push(URL{
					Location: fmt.Sprintf("%s/%s", s.URL, guild.ID),
				})

				channels, err := s.discord.Cabinet.Channels(guild.ID)
				if err != nil {
					errCh1 <- err
				}

				var wg2 sync.WaitGroup
				wg2.Add(len(channels))
				// we first need to go through these channels and ensure their messages
				// are cached
				errCh2 := make(chan error)
				waitCh2 := make(chan bool)
				// level 2 wrapper
				go func() {
					// level 2
					for _, channel := range channels {
						go func(channel discord.Channel) {
							defer wg2.Done()
							if channel.Type != discord.GuildForum {
								return
							}
							me, _ := s.discord.Cabinet.Me()
							perms, err := s.discord.Permissions(channel.ID, me.ID)
							if err != nil {
								errCh2 <- fmt.Errorf("fetching channel permissions for %s: %s", channel.Name, err)
							}
							if !perms.Has(0 |
								discord.PermissionReadMessageHistory |
								discord.PermissionViewChannel) {
								return
							}
							err = s.ensureArchivedThreads(channel.ID)
							if err != nil {
								errCh2 <- fmt.Errorf("fetching archived threads for %s: %s", channel.Name, err)
							}
						}(channel)
					}
					wg2.Wait()
					waitCh2 <- true
				}()
				select {
				case e := <-errCh2:
					{
						errCh1 <- e
					}
				case <-waitCh2:
					{
					}
				}

				// and then go through it again.
				channels, _ = s.discord.Cabinet.Channels(guild.ID)
				var forums []discord.Channel
				for _, channel := range channels {
					if channel.Type != discord.GuildForum {
						continue
					}
					forums = append(forums, channel)
				}
				for _, forum := range forums {
					urls.Push(URL{
						Location: fmt.Sprintf("%s/%s/%s", s.URL, guild.ID, forum.ID),
					})
					var posts []discord.Channel
					for _, t := range channels {
						if t.ParentID == forum.ID &&
							t.Type == discord.GuildPublicThread {
							posts = append(posts, t)
						}
					}
					if len(posts) < 1 {
						continue
					}
					var wg3 sync.WaitGroup
					wg3.Add(len(posts))
					errCh3 := make(chan error)
					waitCh3 := make(chan bool)
					// level 3 wrapper
					go func() {
						for _, post := range posts {
							// level 3
							go func(post discord.Channel) {
								defer wg3.Done()
								// Posts are usually truncated to a certain limit.
								// if this page exceeds said limit, we need to put the
								// paginated version in too.
								var msgs []discord.Message
								var err error
								chunks := 0.0
								if post.MessageCount > paginationLimit {
									chunks = float64(post.MessageCount / paginationLimit)
									msgs, err = s.messageCache.Messages(post.ID)
									if err != nil {
										errCh3 <- err
										return
									}
								}
								urls.Push(URL{
									Location: fmt.Sprintf("%s/%s/%s/%s", s.URL, guild.ID, forum.ID, post.ID),
								})
								for i := 1; float64(i) < chunks; i++ {
									urls.Push(URL{
										Location: fmt.Sprintf("%s/%s/%s/%s?after=%s", s.URL, guild.ID, forum.ID, post.ID, msgs[i*paginationLimit].ID),
									})
								}
							}(post)
						}
						wg3.Wait()
						waitCh3 <- true
					}()

					select {
					case e := <-errCh3:
						{
							errCh2 <- e
						}
					case <-waitCh3:
						{
						}
					}

				}

			}(guild)
		}
		wg1.Wait()
		waitCh1 <- true
	}()
	select {
	case e := <-errCh1:
		{
			return e
		}
	case <-waitCh1:
		{
		}
	}
	for _, url := range urls.urls {
		if err = enc.Encode(url); err != nil {
			return err
		}
	}
	if err = enc.EncodeToken(XMLURLSetEnd); err != nil {
		return err
	}
	if err = enc.Flush(); err != nil {
		return err
	}
	_, err = w.Write([]byte{'\n'})
	return err
}
