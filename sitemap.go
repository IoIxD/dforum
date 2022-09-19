package main

import (
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

const (
	XMLIndexSettings     = `xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"`
	XMLListSettings      = `xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml"`
	XMLPageHeader        = `<?xml version="1.0" encoding="UTF-8"?>`
	XMLURLPageHeader     = `<urlset ` + XMLListSettings + `>`
	XMLSitemapPageHeader = `<sitemapindex ` + XMLIndexSettings + `>`
	XMLSitemapPageFooter = `</sitemapindex>`
	XMLURLPageFooter     = `</urlset>`
)

var dontCare = strings.NewReplacer(
	"sitemap-", "",
	"sitemap", "",
	".xml", "",
)

type CacheObject struct {
	XMLPage     []byte
	LastUpdated int64
	GZipped     bool
}

var Caches map[string]CacheObject

type Sitemap struct {
	Location string `xml:"loc"`
}
type SitemapIndex []Sitemap

func (s *SitemapIndex) Push(index Sitemap) {
	newIndex := append(*s, index)
	s = &newIndex
}

type URL struct {
	Location  string  `xml:"loc"`
	LastMod   string  `xml:"lastmod"`
	Frequency string  `xml:"changefreq"`
	Priority  float32 `xml:"priority"`
}
type URLSet []URL

func (u *URLSet) Push(index URL) {
	newIndex := append(*u, index)
	u = &newIndex
}

var XMLPage string           // The xml page to serve.
var LastUpdatedFormat string // obsolete but i'm too tired to remove it

func init() {
	Caches = make(map[string]CacheObject)
}

func XMLServe(w http.ResponseWriter, r *http.Request, pagename string) {
	var XMLPage []byte
	var gz bool
	obj, ok := Caches[pagename]
	// If it's not existent, cache it.
	if !ok {
		fmt.Printf("%v doesn't exist, caching.\n", pagename)
		XMLPage, gz = XMLPageGen(pagename)

		newOBJ := CacheObject{
			XMLPage:     XMLPage,
			GZipped:     gz,
			LastUpdated: time.Now().Unix(),
		}
		Caches[pagename] = newOBJ
	} else {
		// Otherwise, search the cache.
		lastUpdated := obj.LastUpdated
		// If the time 30 minutes ago is greater then the updated time
		if (time.Now().Unix() - int64(time.Minute*30)) > lastUpdated {
			// Update the cache
			obj.XMLPage, obj.GZipped = XMLPageGen(pagename)
			obj.LastUpdated = time.Now().Unix()
			fmt.Printf("%v is outdated, re-caching.\n", pagename)
		} else {
			// Otherwise, serve that cached version.
			fmt.Printf("%v is cached, serving that.\n", pagename)
		}
		XMLPage, gz = obj.XMLPage, obj.GZipped
	}
	w.Header().Set("Content-Name", pagename)
	if gz {
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", "attachment; filename="+pagename)
	} else {
		w.Header().Set("Content-Type", "text/xml")
		w.Header().Set("Content-Name", pagename)
	}

	w.Write(XMLPage)
}
func XMLPageGen(pagename string) (XMLPage []byte, gz bool) {
	// get the values and stuff we want from the pagename
	pagename = dontCare.Replace(pagename)

	if strings.Contains(pagename, ".gz") {
		pagename = strings.Replace(pagename, ".gz", "", 1)
		gz = true
	} else {
		gz = false
	}

	var XMLResult string
	parts := strings.Split(pagename, "-")
	if parts[0] == "" {
		XMLResult = XMLPageGenGuilds()
	} else {
		var guildID int
		guildID, err := strconv.Atoi(parts[1])
		if err != nil {
			return []byte(err.Error()), false
		}
		XMLResult = XMLPageGenGuildChannelThreads(snowflake.ID(guildID))
	}
	if gz {
		return GZIPString(XMLResult), true
	} else {
		return []byte(XMLResult), false
	}
}

func XMLPageGenGuilds() string {
	var XMLPage bytes.Buffer
	var sitemapIndex SitemapIndex

	guilds := Client.Client.Caches().Guilds().All()
	for _, g := range guilds {
		sitemapIndex.Push(Sitemap{
			Location: fmt.Sprintf("https://dfs.ioi-xd.net/sitemap-%v.xml.gz", g.ID),
		})
	}
	output, err := xml.Marshal(sitemapIndex)
	if err != nil {
		return err.Error()
	}
	XMLPage.Write([]byte(XMLSitemapPageHeader))
	XMLPage.Write(output)
	XMLPage.Write([]byte(XMLSitemapPageFooter))
	return XMLPage.String()
}

func XMLPageGenGuildChannelThreads(guildID snowflake.ID) string {
	var XMLPage bytes.Buffer
	var urlIndex URLSet

	channels := Client.GetForums(guildID)
	for _, c := range channels {
		threads := Client.GetThreadsInChannel(c.ID())
		// todo: have this actually reflect when the channel was last updated.
		lastUpdatedFormat := time.Now().Format(time.RFC3339)
		for _, t := range threads {
			urlIndex.Push(URL{
				Location:  fmt.Sprintf("https://dfs.ioi-xd.net/%v/%v/%v", guildID, c.ID(), t.ID()),
				LastMod:   lastUpdatedFormat,
				Frequency: "hourly",
				Priority:  1.0,
			})
		}
	}
	output, err := xml.Marshal(urlIndex)
	if err != nil {
		return err.Error()
	}
	XMLPage.Write([]byte(XMLURLPageHeader))
	XMLPage.Write(output)
	XMLPage.Write([]byte(XMLURLPageHeader))
	return XMLPage.String()
}

func GZIPString(page string) (result []byte) {
	var b bytes.Buffer
	gzipWriter := gzip.NewWriter(&b)
	_, err := gzipWriter.Write([]byte(page))
	if err != nil {
		fmt.Println(err)
		return
	}
	gzipWriter.Close()
	return b.Bytes()
}
