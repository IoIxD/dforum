package main

import (
	"bytes"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"regexp"

	"github.com/chai2010/webp"
)

var extensionRegexp = regexp.MustCompile(`\.(png|jpeg|jpg|webp|gif)`)

func (s *server) ProxyPage(w http.ResponseWriter, r *http.Request) {
	// the url to encode
	url := r.URL.Query().Get("url")
	if url == "" {
		return
	}
	// the contents of the url
	resp, err := http.Get(url)
	if err != nil {
		w.WriteHeader(500)
		w.Header().Add("Content-Name", "error.html")
		w.Write([]byte(err.Error()))
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
		w.Header().Set("Content-Type", "image/webp")
		w.Header().Set("Content-Disposition", "attachment; filename="+url)
		w.WriteHeader(200)
		var fuck []byte
		_, _ = resp.Body.Read(fuck)
		w.Write(fuck)
	}

	if err != nil {
		w.WriteHeader(500)
		w.Header().Add("Content-Name", "error.html")
		w.Write([]byte(err.Error()))
		return
	}

	var webpImage bytes.Buffer
	err = webp.Encode(&webpImage, image, &webp.Options{
		Quality: 25,
	})
	if err != nil {
		w.WriteHeader(500)
		w.Header().Add("Content-Name", "error.html")
		w.Write([]byte(err.Error()))
		return
	}

	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Content-Disposition", "attachment; filename="+url+".webp")
	w.WriteHeader(200)
	w.Write(webpImage.Bytes())
}
