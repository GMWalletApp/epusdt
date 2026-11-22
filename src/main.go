package main

import (
	"embed"
	"io/fs"
	"net/http"
	"os"

	"github.com/GMWalletApp/epusdt/command"
	"github.com/gookit/color"
)

//go:embed all:www
var wwwDir embed.FS

func main() {
	defer func() {
		if failure := recover(); failure != nil {
			color.Error.Println("[Start Server Err!!!] ", failure)
			os.Exit(1)
		}
	}()

	frontend, err := fs.Sub(wwwDir, "www")
	if err != nil {
		panic(err)
	}

	if err := command.Execute(http.FS(frontend)); err != nil {
		panic(err)
	}
}
