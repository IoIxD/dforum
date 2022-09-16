package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/disgoorg/snowflake/v2"
)

const (
	XMLPageHeader = `<?xml version="1.0" encoding="UTF-8"?>
	<urlset xmlns:xhtml="http://www.w3.org/1999/xhtml" xmlns:image="http://www.google.com/schemas/sitemap-image/1.1" xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`
)

var dontCare = strings.NewReplacer(
	"sitemap-", "",
	"sitemap", "",
	".xml", "",
)

var XMLPage string           // The xml page to serve.
var LastUpdated int64        // When the xml page was last generated
var LastUpdatedFormat string // That same value but formatted in a way that Google likes.

func XMLServe(w http.ResponseWriter, r *http.Request, pagename string) {
	//timeNow := time.Now().Unix()
	// If it was last updated up to 30 minutes ago...
	/*if (timeNow-int64(time.Minute*30)) > LastUpdated || LastUpdated == 0 {
		// Refresh it.
		LastUpdated = timeNow
		LastUpdatedFormat = time.Now().Format("2006-01-02")
		XMLPage = XMLPageGen(pagename)
	}*/
	XMLPage, gz := XMLPageGen(pagename)
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
		if parts[0] == "" {
			XMLResult = XMLPageGenGuilds()
		} else {
			// convert the guild id to a snowflake
			guildID, err := strconv.Atoi(parts[0])
			if err != nil {
				fmt.Println(err)
				return
			}
			XMLResult = XMLPageGenGuildChannels(snowflake.ID(guildID))
		}
	case 2:
		// convert the channel id to a snowflake
		chanID, err := strconv.Atoi(parts[1])
		if err != nil {
			fmt.Println(err)
			return
		}
		XMLResult = XMLPageGenGuildChannelThreads(parts[0], snowflake.ID(chanID))
	}
	if gz {
		return GZIPString(XMLResult), true
	} else {
		return []byte(XMLResult), false
	}
}

func XMLPageGenGuilds() (XMLPage string) {
	XMLPage = XMLPageHeader
	XMLPage += `
		<url>
			<loc>https://dfs.ioi-xd.net/</loc>
			<lastmod>` + LastUpdatedFormat + `</lastmod>
			<changefreq>hourly</changefreq>
			<priority>1.0</priority>
		</url>`

	guilds := Client.Client.Caches().Guilds().All()
	for _, g := range guilds {
		XMLPage += fmt.Sprintf(`
			<url>
				<loc>https://dfs.ioi-xd.net/sitemap-%v.xml.gz</loc>
				<lastmod>%v</lastmod>
				<changefreq>hourly</changefreq>
				<priority>1.0</priority>
			</url>
		`, g.ID, LastUpdatedFormat)
	}
	XMLPage += `</urlset>`
	return
}

func XMLPageGenGuildChannels(guildID snowflake.ID) (XMLPage string) {
	channels := Client.GetForums(guildID)
	for _, t := range channels {
		XMLPage += fmt.Sprintf(`
			<url>
				<loc>https://dfs.ioi-xd.net/sitemap-%v-%v.xml.gz</loc>
				<lastmod>%v</lastmod>
				<changefreq>hourly</changefreq>
				<priority>1.0</priority>
			</url>
		`, guildID, t.ID(), LastUpdatedFormat)
	}
	XMLPage += `</urlset>`
	return
}
func XMLPageGenGuildChannelThreads(guildID string, chanID snowflake.ID) (XMLPage string) {
	XMLPage = XMLPageHeader
	threads := Client.GetThreadsInChannel(chanID)
	for _, t := range threads {
		XMLPage += fmt.Sprintf(`
			<url>
				<loc>https://dfs.ioi-xd.net/%v/%v/%v</loc>
				<lastmod>%v</lastmod>
				<changefreq>hourly</changefreq>
				<priority>1.0</priority>
			</url>
		`, guildID, chanID, t.ID(), LastUpdatedFormat)
	}
	XMLPage += `</urlset>`
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
