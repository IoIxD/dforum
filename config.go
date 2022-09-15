package main

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

var LocalConfig struct {
	BotToken string
}

func init() {
	file, err := os.ReadFile("config.toml")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	toml.Unmarshal(file, &LocalConfig)
}
