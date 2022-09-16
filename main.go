package main

import (
	"context"
	"embed"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

//go:embed pages/*.*
var pages embed.FS
var tmpl *template.Template
var re *regexp.Regexp

func main() {
	// initialize the discord shit
	bot := InitBot()
	defer bot.Client.Close(context.TODO())

	// initialize the template shit
	tmpl = template.New("")
	tmpl.Funcs(FuncMap(bot)) // "FuncMap" refers to a template.FuncMap in another file, that isn't included in this one.
	_, err := tmpl.ParseFS(pages, "pages/*")
	if err != nil {
		log.Println(err)
	}

	// initialize the regex shit
	re = regexp.MustCompile(`([^0-9./])`)

	// initialize the main server
	s := &http.Server{
		Addr:           ":8084",
		Handler:        http.HandlerFunc(handlerFunc),
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	if err = s.ListenAndServe(); err != nil {
		log.Fatalln(err)
	}
}

func handlerFunc(w http.ResponseWriter, r *http.Request) {
	// How are we trying to access the site?
	switch r.Method {
	case http.MethodGet, http.MethodHead: // These methods are allowed. continue.
	default: // Send them an error for other ones.
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	// Get the pagename.
	pagename, values := getPagename(r.URL.EscapedPath())

	var internal bool
	var filename string

	var file *os.File
	var err error

	// If the pagename is all numbers then it's a list page.
	if !re.Match([]byte(pagename)) {
		pagename = "list"
	}

	// Check if it could refer to another internal page
	if file, err = os.Open("pages/" + pagename + ".gohtml"); err == nil {
		filename = "pages/" + pagename + ".gohtml"
		internal = true
		// Otherwise, check if it could refer to a regular file.
	} else {
		if file, err = os.Open("./" + pagename); err == nil {
			filename = "./" + pagename
		} else {
			// If all else fails, send a 404.
			http.Error(w, err.Error(), 404)
			return
		}
	}

	// get the mime-type.
	contentType, err := GetContentType(file)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Name", filename)
	w.WriteHeader(200)

	var Info struct {
		Values []string
		Query  url.Values
	}
	Info.Values = values
	Info.Query = r.URL.Query()

	// Serve the file differently based on whether it's an internal page or not.
	if internal {
		if err := tmpl.ExecuteTemplate(w, pagename+".gohtml", Info); err != nil {
			http.Error(w, err.Error(), 500)
		}
	} else {
		page, err := os.ReadFile(filename)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Write(page)
	}
}

func getPagename(fullpagename string) (string, []string) {
	// Split the pagename into sections
	if fullpagename[0] == '/' && len(fullpagename) > 1 {
		fullpagename = fullpagename[1:]
	}
	values_ := strings.Split(fullpagename, "/")
	// Filter the values to ones that aren't blank
	values := make([]string, 0)
	for _, v := range values_ {
		if v != "" {
			values = append(values, v)
		}
	}
	if len(values) == 0 {
		values = append(values, "index")
	}

	// Then try and get the relevant pagename from that, accounting for many specifics.
	pagename := values[0]
	switch pagename {
	// If it's blank, set it to the default page.
	case "":
		return "index", values
	// If the first part is resources, then treat the rest of the url normally
	case "resources":
		return fullpagename, values
	}
	return pagename, values
}

func GetContentType(output *os.File) (string, error) {
	ext := filepath.Ext(output.Name())
	file := make([]byte, 1024)
	switch ext {
	case ".htm", ".html", ".gohtm", ".gohtml":
		return "text/html", nil
	case ".css":
		return "text/css", nil
	case ".js":
		return "application/javascript", nil
	default:
		_, err := output.Read(file)
		if err != nil {
			return "", err
		}
		return http.DetectContentType(file), nil
	}
}
