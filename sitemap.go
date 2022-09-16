package main

import (
	"fmt"
	"net/http"
	"time"
)

var XMLPage string    // The xml page to serve.
var LastUpdated int64 // When the xml page was last generated

func XMLServe(w http.ResponseWriter, r *http.Request) {
	timeNow := time.Now().Unix()
	// If it was last updated up to 30 minutes ago...
	if (timeNow-int64(time.Minute*30)) > LastUpdated || LastUpdated == 0 {
		// Refresh it.
		LastUpdated = timeNow
		XMLPage = XMLPageGen()
	}

	w.Header().Set("Content-Type", "text/xml")
	w.Header().Set("Content-Name", "sitemap.xml")

	w.Write([]byte(XMLPage))
}
func XMLPageGen() (XMLPage string) {
	XMLPage = `<?xml version="1.0" encoding="UTF-8"?>
		<urlset xmlns:xhtml="http://www.w3.org/1999/xhtml" xmlns:image="http://www.google.com/schemas/sitemap-image/1.1" xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
	`
	guilds := GetGuilds()
	for _, g := range guilds {
		channels := g.Channels
		for _, c := range channels {
			if c.Type != 15 && c.Type != 11 {
				continue
			}
			threads := GetThreadsInChannel(g.ID, c.ID)
			if threads.Error != nil {
				fmt.Println(threads.Error)
				return
			}
			for _, t := range threads.Channels {
				XMLPage += fmt.Sprintf(`
					<url>
						<loc>https://dfs.ioi-xd.net/%v/%v/%v</loc>
						<lastmod>%d</lastmod>
						<changefreq>hourly</changefreq>
						<priority>1.0</priority>
					</url>
				`, g.ID, c.ID, t.ID, LastUpdated)
			}
		}
	}
	XMLPage += `</urlset>`
	return
}
