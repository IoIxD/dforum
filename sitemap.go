package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"strings"
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
			XMLResult = XMLPageGenGuildChannels(parts[0])
		}
	case 2:
		XMLResult = XMLPageGenGuildChannelThreads(parts[0], parts[1])
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

	guilds := discord.State.Guilds
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

func XMLPageGenGuildChannels(guildID string) (XMLPage string) {
	XMLPage = XMLPageHeader
	guild, err := discord.State.Guild(guildID)
	if err != nil {
		fmt.Println(err)
		return
	}
	channels := guild.Channels
	for _, t := range channels {
		if t.Type != 15 && t.Type != 11 {
			continue
		}
		XMLPage += fmt.Sprintf(`
			<url>
				<loc>https://dfs.ioi-xd.net/sitemap-%v-%v.xml.gz</loc>
				<lastmod>%v</lastmod>
				<changefreq>hourly</changefreq>
				<priority>1.0</priority>
			</url>
		`, guildID, t.ID, LastUpdatedFormat)
	}
	XMLPage += `</urlset>`
	return
}
func XMLPageGenGuildChannelThreads(guildID, chanID string) (XMLPage string) {
	XMLPage = XMLPageHeader
	threads := GetThreadsInChannel(guildID, chanID)
	if threads.Error != nil {
		fmt.Println(threads.Error)
		return
	}
	for _, t := range threads.Channels {
		XMLPage += fmt.Sprintf(`
			<url>
				<loc>https://dfs.ioi-xd.net/%v/%v/%v</loc>
				<lastmod>%v</lastmod>
				<changefreq>hourly</changefreq>
				<priority>1.0</priority>
			</url>
		`, guildID, chanID, t.ID, LastUpdatedFormat)
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
