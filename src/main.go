package main

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/GMWalletApp/epusdt/command"
	"github.com/gookit/color"
)

//go:embed all:www
var wwwDir embed.FS

func main() {
	frontend, err := fs.Sub(wwwDir, "www")
	if err != nil {
		panic(err)
	}

	defer func() {
		if err := recover(); err != nil {
			color.Error.Println("[Start Server Err!!!] ", err)
		}
	}()
	if err := command.Execute(http.FS(frontend)); err != nil {
		panic(err)
	}
}
