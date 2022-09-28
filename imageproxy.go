package main

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"regexp"

	"github.com/Kagami/go-avif"
	"github.com/chai2010/webp"
)

var extensionRegexp = regexp.MustCompile(`\.(png|jpeg|jpg|webp|gif)`)

func (s *server) ProxyPage(w http.ResponseWriter, r *http.Request) {
	// forcefully branch into a thread
	ready := make(chan int, 1)
	var finalImage bytes.Buffer
	var format, url string
	var err error

	go func(ready chan int, finalImage *bytes.Buffer) {
		// the format to encode to
		format = r.URL.Query().Get("format")
		if format == "" || (format != "webp" && format != "avif") {
			format = "webp"
		}

		// the url to encode
		url := r.URL.Query().Get("url")
		if url == "" {
			return
		}
		var resp *http.Response

		// the contents of the url
		resp, err = http.Get(url + "?size=256")
		if err != nil {
			ready <- 1
			return
		}

		// the file extension
		ext := string(extensionRegexp.Find([]byte(url)))

		var image image.Image

		switch ext {
		case ".png":
			image, err = png.Decode(resp.Body)
		case ".jpeg", ".jpg":
			image, err = jpeg.Decode(resp.Body)
		case ".gif":
			image, err = gif.Decode(resp.Body)
		case ".webp":
			if format == "webp" {
				w.Header().Set("Content-Type", "image/webp")
				w.Header().Set("Content-Disposition", "attachment; filename="+url)
				w.WriteHeader(200)
				var fuck []byte
				_, _ = resp.Body.Read(fuck)
				w.Write(fuck)
			} else {
				image, err = webp.Decode(resp.Body)
			}
		}

		if err != nil {
			ready <- 1
			return
		}

		switch format {
		case "webp":
			err = webp.Encode(finalImage, image, &webp.Options{
				Quality: 25,
			})
		case "avif":
			err = avif.Encode(finalImage, image, &avif.Options{
				Threads: 2,
				Quality: 63,
			})
		}
		ready <- 1
	}(ready, &finalImage)
	_ = <-ready

	if err != nil {
		w.WriteHeader(500)
		w.Header().Set("Content-Type", "text/html")
		w.Header().Add("Content-Name", "error.html")
		w.Write([]byte(err.Error()))
		return
	}

	w.Header().Set("Content-Type", "image/"+format)
	w.Header().Set("Content-Disposition", "attachment; filename="+url+format)
	w.WriteHeader(200)
	w.Write(finalImage.Bytes())
}
