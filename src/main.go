package main

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"os"

	"github.com/GMWalletApp/epusdt/command"
	"github.com/GMWalletApp/epusdt/internal/selfupdate"
	"github.com/gookit/color"
)

//go:embed all:www
var wwwDir embed.FS

func main() {
	defer func() {
		if failure := recover(); failure != nil {
			restored, rollbackErr := selfupdate.RollbackPendingUpdate()
			if restored {
				fmt.Fprintf(os.Stderr, "startup failed; restored previous binary: %v\n", failure)
				os.Exit(selfupdate.RestartExitCode())
			}
			if rollbackErr != nil {
				fmt.Fprintf(os.Stderr, "startup rollback failed: %v\n", rollbackErr)
			}
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
