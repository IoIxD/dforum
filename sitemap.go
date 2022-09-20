package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
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
		sitemap := make([]byte, buf.Len())
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
		{xml.Name{Local: "xmlns"}, "http://www.sitemaps.org/schemas/sitemap/0.9"},
	},
}

var XMLURLSetEnd = XMLURLSetStart.End()

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
	for _, guild := range guilds {
		if err = enc.Encode(URL{
			Location: fmt.Sprintf("https://dfs.ioi-xd.net/%s", guild.ID),
		}); err != nil {
			return err
		}

		channels, err := s.discord.Channels(guild.ID)
		if err != nil {
			return err
		}
		threads, err := s.discord.ActiveThreads(guild.ID)
		if err != nil {
			return err
		}
		for _, channel := range channels {
			if channel.Type != discord.GuildForum {
				continue
			}
			if err = enc.Encode(URL{
				Location: fmt.Sprintf("https://dfs.ioi-xd.net/%s/%s", guild.ID, channel.ID),
			}); err != nil {
				return err
			}
		}
		for _, thread := range threads.Threads {
			for _, channel := range channels {
				if channel.Type != discord.GuildForum {
					continue
				}
				if thread.ParentID != channel.ID {
					continue
				}
				if err = enc.Encode(URL{
					Location: fmt.Sprintf("https://dfs.ioi-xd.net/%s/%s/%s", guild.ID, channel.ID, thread.ID),
				}); err != nil {
					return err
				}
				break
			}
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
