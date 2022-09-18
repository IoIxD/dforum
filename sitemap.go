package main

import (
	"bytes"
	"compress/gzip"
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

var XMLPage string           // The xml page to serve.
var LastUpdatedFormat string // obsolete but i'm too tired to remove it

func init() {
	Caches = make(map[string]CacheObject)
}

func XMLServe(w http.ResponseWriter, r *http.Request, pagename string) {
	// TODO: caching system
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
	switch len(parts) {
	case 0:
		XMLResult = XMLPageGenGuilds()
	case 1:
		fmt.Println(parts)
		if parts[0] == "" {
			XMLResult = XMLPageGenGuilds()
		} else {
			// convert the guild id to a snowflake
			guildID, err := strconv.Atoi(parts[0])
			if err != nil {
				fmt.Println(err)
				break
			}
			XMLResult = XMLPageGenGuildChannels(snowflake.ID(guildID))
		}
	case 2:
		// convert the guild id to a snowflake
		guildID, err := strconv.Atoi(parts[0])
		if err != nil {
			fmt.Println(err)
			break
		}
		// convert the channel id to a snowflake
		chanID, err := strconv.Atoi(parts[1])
		if err != nil {
			fmt.Println(err)
			break
		}
		XMLResult = XMLPageGenGuildChannelThreads(snowflake.ID(guildID), snowflake.ID(chanID))
	}
	if gz {
		return GZIPString(XMLResult), true
	} else {
		return []byte(XMLResult), false
	}
}

func XMLPageGenGuilds() (XMLPage string) {
	XMLPage = XMLPageHeader
	XMLPage += XMLSitemapPageHeader
	guilds := Client.Client.Caches().Guilds().All()
	for _, g := range guilds {
		XMLPage += fmt.Sprintf(`
			<sitemap>
				<loc>https://dfs.ioi-xd.net/sitemap-%v.xml.gz</loc>
			</sitemap>
		`, g.ID)
	}
	XMLPage += XMLSitemapPageFooter
	return
}

func XMLPageGenGuildChannels(guildID snowflake.ID) (XMLPage string) {
	XMLPage = XMLPageHeader
	XMLPage += XMLSitemapPageHeader
	channels := Client.GetForums(guildID)
	for _, t := range channels {
		XMLPage += fmt.Sprintf(`
			<sitemap>
				<loc>https://dfs.ioi-xd.net/sitemap-%v-%v.xml.gz</loc>
			</sitemap>
		`, guildID, t.ID())
	}
	XMLPage += XMLSitemapPageFooter
	return
}
func XMLPageGenGuildChannelThreads(guildID, chanID snowflake.ID) (XMLPage string) {
	XMLPage = XMLPageHeader
	XMLPage += XMLURLPageHeader
	threads := Client.GetThreadsInChannel(chanID)
	// todo: have this actually reflect when the channel was last updated.
	lastUpdatedFormat := time.Now().Format(time.RFC3339)
	for _, t := range threads {

		XMLPage += fmt.Sprintf(`
			<url>
				<loc>https://dfs.ioi-xd.net/%v/%v/%v</loc>
				<lastmod>%v</lastmod>
				<changefreq>hourly</changefreq>
				<priority>1.0</priority>
			</url>
		`, guildID, chanID, t.ID(), lastUpdatedFormat)
	}
	XMLPage += XMLURLPageFooter
	return
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
