package command

import (
	"net/http"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/spf13/cobra"
)

var configPath string

var rootCmd = &cobra.Command{}

func Execute(frontend http.FileSystem) error {
	rootCmd.AddCommand(newHTTPCommand(frontend))
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "path to .env or directory containing .env")
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		config.SetConfigPath(configPath)
	}
}
