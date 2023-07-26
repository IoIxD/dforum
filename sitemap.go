package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"
)

const LIMIT = 250

type URL struct {
	XMLName   string  `xml:"url"`
	Location  string  `xml:"loc"`
	LastMod   string  `xml:"lastmod,omitempty"`
	Frequency string  `xml:"changefreq,omitempty"`
	Priority  float32 `xml:"priority,omitempty"`
}

type SitemapEntry struct {
	XMLName   string  `xml:"sitemap"`
	Location  string  `xml:"loc"`
	LastMod   string  `xml:"lastmod,omitempty"`
	Frequency string  `xml:"changefreq,omitempty"`
	Priority  float32 `xml:"priority,omitempty"`
}

func (s *server) getSitemap(w http.ResponseWriter, r *http.Request) {
	s.sitemapMu.Lock()
	var sitemap []byte
	var modtime time.Time
	offset := r.URL.Query().Get("offset")
	if s.sitemaps[offset] == nil || time.Since(s.sitemapUpdated) > 6*time.Hour {
		buf := s.buffers.Get().(*bytes.Buffer)
		err := s.writeSitemap(buf, offset)
		if err != nil {
			s.sitemapMu.Unlock()
			buf.Reset()
			s.buffers.Put(buf)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sitemap = make([]byte, buf.Len())
		copy(sitemap, buf.Bytes())
		s.sitemaps[offset] = sitemap
		s.sitemapUpdated = time.Now()
		modtime = s.sitemapUpdated
		s.sitemapMu.Unlock()
		buf.Reset()
		s.buffers.Put(buf)
	} else {
		sitemap = s.sitemaps[offset]
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

var SitemapSetStart = xml.StartElement{
	Name: xml.Name{Local: "sitemapindex"},
	Attr: []xml.Attr{
		{Name: xml.Name{Local: "xmlns"}, Value: "http://www.sitemaps.org/schemas/sitemap/0.9"},
	},
}

var XMLURLSetEnd = XMLURLSetStart.End()
var SitemapSetEnd = SitemapSetStart.End()

var guildList map[int]URL = make(map[int]URL, 0)
var chanList map[int]URL = make(map[int]URL, 0)
var postList map[int]URL = make(map[int]URL, 0)

func (s *server) writeSitemap(w io.Writer, offset string) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	var err error
	if offset == "" {
		if err = enc.EncodeToken(SitemapSetStart); err != nil {
			return err
		}
	} else {
		if err = enc.EncodeToken(XMLURLSetStart); err != nil {
			return err
		}
	}

	i1, i2, i3 := 0, 0, 0
	guilds, _ := s.discord.Cabinet.Guilds()
	me, _ := s.discord.Cabinet.Me()
	if _, ok := guildList[0]; !ok || time.Since(s.sitemapUpdated) > 6*time.Hour {
		for _, guild := range guilds {
			guildList[i1] = URL{
				Location: fmt.Sprintf("%s/%s", s.URL, guild.ID),
			}
			memberSelf, err := s.discord.Member(guild.ID, me.ID)
			if err != nil {
				return fmt.Errorf("error fetching self as member: %w", err)
			}
			channels, err := s.channels(guild.ID)
			if err != nil {
				return fmt.Errorf("error fetching channels: %w", err)
			}
			for _, forum := range channels {
				if forum.Type != discord.GuildForum {
					continue
				}
				perms := discord.CalcOverwrites(guild, forum, *memberSelf)
				if !perms.Has(0 |
					discord.PermissionReadMessageHistory |
					discord.PermissionViewChannel) {
					continue
				}
				chanList[i2] = URL{
					Location: fmt.Sprintf("%s/%s/%s", s.URL, guild.ID, forum.ID),
				}
				for _, post := range channels {
					if post.ParentID != forum.ID ||
						post.Type != discord.GuildPublicThread {
						continue
					}
					postList[i3] = URL{
						Location: fmt.Sprintf("%s/%s/%s/%s", s.URL, guild.ID, forum.ID, post.ID),
					}
					i3++
				}
				i2++
			}
			i1++
		}
		if time.Since(s.sitemapUpdated) > 6*time.Hour {
			s.sitemapUpdated = time.Now()
		}
	}

	if offset == "" {
		// put a url for every possible "offset url" there might be
		lenAll := len(guildList) + len(chanList) + len(postList)
		parts := lenAll % LIMIT
		for i := 0; i <= parts; i++ {
			if err = enc.Encode(SitemapEntry{
				Location: fmt.Sprintf("%s/sitemap.xml?offset=%d", s.URL, i*LIMIT),
			}); err != nil {
				return err
			}
		}
		if err = enc.EncodeToken(SitemapSetEnd); err != nil {
			return err
		}
	} else {
		offset, err := strconv.Atoi(offset)
		if err != nil {
			return err
		}

		for i := 0; i <= len(guildList); i++ {
			if i < offset {
				continue
			}
			if i > offset+LIMIT {
				break
			} else {
				if err = enc.Encode(guildList[i]); err != nil {
					return err
				}
			}
		}
		for i := 0; i <= len(chanList); i++ {
			if i < offset {
				continue
			}
			if i > offset+LIMIT {
				break
			} else {
				if err = enc.Encode(chanList[i]); err != nil {
					return err
				}
			}
		}
		for i := 0; i <= len(postList); i++ {
			if i < offset {
				continue
			}
			if i > offset+LIMIT {
				break
			} else {
				if err = enc.Encode(postList[i]); err != nil {
					return err
				}
			}
		}
		if err = enc.EncodeToken(XMLURLSetEnd); err != nil {
			return err
		}
	}

	if err = enc.Flush(); err != nil {
		return err
	}
	_, err = w.Write([]byte{'\n'})
	return err
}
