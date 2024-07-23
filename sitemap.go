package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/go-chi/chi/v5/middleware"
)

type Sitemap struct {
	XMLName xml.Name `xml:"sitemap"`
	Loc     string   `xml:"loc"`
}

type URL struct {
	XMLName   string  `xml:"url"`
	Location  string  `xml:"loc"`
	LastMod   string  `xml:"lastmod,omitempty"`
	Frequency string  `xml:"changefreq,omitempty"`
	Priority  float32 `xml:"priority,omitempty"`
}

const (
	XMLSitemapIndexStart = `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`
	XMLSitemapIndexEnd   = `</sitemapindex>`
	XMLURLSetStart       = `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`
	XMLURLSetEnd         = `</urlset>`
)

const MaxSitemapURLs = 50000
const MaxSitemapSize = 52_428_800

func (s *server) getSitemap(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/sitemap.xml" {
		var wr = middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		http.ServeFile(wr, r, path.Join(s.SitemapDir, "sitemap.xml"))
		if wr.Status() == http.StatusNotFound {
			go func() {
				s.updateSitemap <- struct{}{}
			}()
		}
		return
	}
	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/sitemap/")
	http.ServeFile(w, r, path.Join(s.SitemapDir, r.URL.Path))
}

func (s *server) writeSitemap() error {
	dir := s.SitemapDir
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	var files []io.Closer
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()

	var sitemapCount int

	var buffer bytes.Buffer
	sitemapEnc := xml.NewEncoder(&buffer)
	var sitemap *os.File
	var sitemapLen int
	var sitemapURLs int
	var encode func(URL) error
	_encode := func(u URL) error {
		if sitemap == nil {
			sitemapCount++
			sitemapPath := filepath.Join(dir, fmt.Sprintf("sitemap%d.xml", sitemapCount))
			var err error
			sitemap, err = os.Create(sitemapPath)
			if err != nil {
				return err
			}
			files = append(files, sitemap)
			if _, err := io.WriteString(sitemap, xml.Header); err != nil {
				return err
			}
			if _, err := io.WriteString(sitemap, XMLURLSetStart); err != nil {
				return err
			}
			sitemapLen = len(xml.Header) + len(XMLURLSetStart)
			sitemapURLs = 0
		}
		if err := sitemapEnc.Encode(u); err != nil {
			return err
		}
		sitemapLen += buffer.Len()
		sitemapURLs++
		if sitemapLen+len(XMLURLSetEnd) > MaxSitemapSize || sitemapURLs > MaxSitemapURLs {
			if _, err := io.WriteString(sitemap, XMLURLSetEnd); err != nil {
				return err
			}
			sitemap.Close()
			buffer.Reset()
			sitemap = nil
			sitemapLen = 0
			return encode(u)
		}
		_, err := buffer.WriteTo(sitemap)
		return err
	}
	encode = _encode
	guilds, _ := s.discord.Cabinet.Guilds()
	me, _ := s.discord.Cabinet.Me()
	for _, guild := range guilds {
		if err := encode(URL{
			Location: fmt.Sprintf("%s/%s", s.URL, guild.ID),
		}); err != nil {
			return err
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
			if err = encode(URL{
				Location: fmt.Sprintf("%s/%s/%s", s.URL, guild.ID, forum.ID),
			}); err != nil {
				return err
			}
		}
		for _, post := range channels {
			if post.Type != discord.GuildPublicThread {
				continue
			}
			if err = encode(URL{
				Location: fmt.Sprintf("%s/%s/%s/%s", s.URL, guild.ID, post.ParentID, post.ID),
			}); err != nil {
				return err
			}
		}
	}
	if sitemap != nil {
		if _, err := io.WriteString(sitemap, XMLURLSetEnd); err != nil {
			return err
		}
		sitemap.Close()
		buffer.Reset()
		sitemap = nil
	}
	index := filepath.Join(dir, "sitemap.xml")
	w, err := os.Create(index)
	if err != nil {
		return err
	}
	defer w.Close()
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	if _, err := io.WriteString(w, XMLSitemapIndexStart); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	for i := 0; i < sitemapCount; i++ {
		if err = enc.Encode(Sitemap{
			Loc: fmt.Sprintf("%s/sitemap/sitemap%d.xml", s.URL, i+1),
		}); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, XMLSitemapIndexEnd); err != nil {
		return err
	}
	if _, err = w.Write([]byte{'\n'}); err != nil {
		return err
	}
	return w.Close()
}
