package main

import (
	"context"
	"embed"
	"flag"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/state"
	"github.com/naoina/toml"
)

//go:embed resources
var embedfs embed.FS
var tmpl *template.Template

func main() {
	cfgpath := flag.String("config", "config.toml", "path to config.toml")
	flag.Parse()
	file, err := os.ReadFile(*cfgpath)
	if err != nil {
		log.Fatalln("Error while reading config:", file)
	}
	config := struct {
		BotToken   string
		ListenAddr string
		Resources  string
	}{ListenAddr: ":8084"}
	if err := toml.Unmarshal(file, &config); err != nil {
		log.Fatalln("Error while parsing config:", err)
	}

	var fsys fs.FS
	if config.Resources != "" {
		fsys = os.DirFS(config.Resources)
	} else {
		if fsys, err = fs.Sub(embedfs, "resources"); err != nil {
			log.Fatalln("Error while using embedded resources:")
		}
	}

	tmpl = template.New("")
	tmpl.Funcs(funcMap)
	_, err = tmpl.ParseFS(fsys, "templates/*")
	if err != nil {
		log.Fatalln("Error parsing templates:", err)
	}

	ctx, done := signal.NotifyContext(context.Background(), os.Interrupt)
	defer done()

	state := state.New("Bot " + config.BotToken)
	state.AddIntents(0 |
		gateway.IntentGuildMessages |
		gateway.IntentGuilds |
		gateway.IntentGuildMembers,
	)
	if err = state.Open(ctx); err != nil {
		log.Fatalln("Error while opening gateway connection to Discord:", err)
	}
	self, err := state.Me()
	if err != nil {
		log.Fatalln("Error fetching self:", err)
	}
	log.Printf("Connected to Discord as %s#%s (%s)\n", self.Username, self.Discriminator, self.ID)

	server := newServer(state, fsys)
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
