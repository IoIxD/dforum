package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/IoIxD/dforum/database"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/state"
	"github.com/diamondburned/arikawa/v3/utils/httputil/httpdriver"
	"github.com/naoina/toml"
)

//go:embed resources
var embedfs embed.FS

type config struct {
	BotToken         string
	ListenAddr       string
	Resources        string
	SiteURL          string
	ServiceName      string
	ServerHostedIn   string
	SitemapDir       string
	ReloadTemplates  bool
	TraceDiscordREST bool
	Database         string
}

type TraceClient struct {
	httpdriver.Client
}

func (c TraceClient) Do(req httpdriver.Request) (httpdriver.Response, error) {
	then := time.Now()
	resp, err := c.Client.Do(req)
	log.Printf("Discord REST: %s in %s", req.GetPath(), time.Since(then))
	return resp, err
}

func main() {
	cfgpath := flag.String("config", "config.toml", "path to config.toml")
	flag.Parse()
	file, err := os.ReadFile(*cfgpath)
	if err != nil {
		log.Fatalln("Error while reading config:", file)
	}
	config := config{ListenAddr: ":8084"}
	if err := toml.Unmarshal(file, &config); err != nil {
		log.Fatalln("Error while parsing config:", err)
	}
	if !strings.HasPrefix(config.Database, "postgres://") {
		log.Fatalln("Config option 'Database' does not begin with postgres://", config.Database)
	}
	var fsys fs.FS
	if config.Resources != "" {
		fsys = os.DirFS(config.Resources)
	} else {
		config.ReloadTemplates = false
		if fsys, err = fs.Sub(embedfs, "resources"); err != nil {
			log.Fatalln("Error while using embedded resources:")
		}
	}
	var tmplfn ExecuteTemplateFunc
	if config.ReloadTemplates {
		tmplfn = func(wr io.Writer, name string, data interface{}) error {
			tmpl := template.New("")
			tmpl.Funcs(funcMap)
			_, err = tmpl.ParseFS(fsys, "templates/*")
			if err != nil {
				return err
			}
			tmpl.Funcs(funcMap)
			return tmpl.ExecuteTemplate(wr, name, data)
		}
	} else {
		tmpl := template.New("")
		tmpl.Funcs(funcMap)
		_, err = tmpl.ParseFS(fsys, "templates/*")
		if err != nil {
			log.Fatalln("Error parsing templates:", err)
		}
		tmplfn = tmpl.ExecuteTemplate
	}

	ctx, done := signal.NotifyContext(context.Background(), os.Interrupt)
	defer done()

	state := state.New("Bot " + config.BotToken)
	if config.TraceDiscordREST {
		state.Client.Client.Client = TraceClient{state.Client.Client.Client}
	}
	state.AddIntents(0 |
		gateway.IntentGuildMessages |
		gateway.IntentGuilds |
		gateway.IntentGuildMembers,
	)
	db, err := database.OpenPostgres(config.Database)
	if err != nil {
		log.Fatalln("Opening database connection:", err)
	}
	server, err := newServer(state, fsys, db, config)
	if err != nil {
		fmt.Println(err)
		return
	}
	ready, cancel := state.ChanFor(func(e interface{}) bool {
		_, ok := e.(*gateway.ReadyEvent)
		return ok
	})
	if err = state.Open(ctx); err != nil {
		log.Fatalln("Error while opening gateway connection to Discord:", err)
	}
	self, err := state.Me()
	if err != nil {
		log.Fatalln("Error fetching self:", err)
	}
	select {
	case <-ready:
	case <-ctx.Done():
		return
	}
	cancel()
	go server.UpdateSitemap()
	log.Printf("Connected to Discord as %s#%s (%s)\n", self.Username, self.Discriminator, self.ID)
	server.executeTemplateFn = tmplfn
	httpserver := &http.Server{
		Addr:           config.ListenAddr,
		Handler:        server,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	httperr := make(chan error, 1)
	go func() {
		httperr <- httpserver.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		done()
		err := httpserver.Shutdown(context.Background())
		if err != nil {
			log.Fatalln("HTTP server shutdown:", err)
		}
	case err := <-httperr:
		if err != nil {
			log.Fatalln("HTTP server encountered error:", err)
		}
	}
}
