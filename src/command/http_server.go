package command

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/GMWalletApp/epusdt/bootstrap"
	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/install"
	"github.com/GMWalletApp/epusdt/internal/selfupdate"
	"github.com/GMWalletApp/epusdt/middleware"
	"github.com/GMWalletApp/epusdt/route"
	"github.com/GMWalletApp/epusdt/task"
	luluHttp "github.com/GMWalletApp/epusdt/util/http"
	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newHTTPCommand(frontend http.FileSystem) *cobra.Command {
	httpCmd := &cobra.Command{
		Use:   "http",
		Short: "http service",
		Long:  "http service commands",
		Run: func(cmd *cobra.Command, args []string) {
		},
	}

	httpCmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "start",
		Long:  "start http service",
		Run: func(cmd *cobra.Command, args []string) {
			// If no config file exists, or if install=true is set in the config,
			// run the first-run install API on the same port as the main server.
			// The wizard writes the .env (with install=false) and shuts itself
			// down so bootstrap.InitApp() can read it normally on the same port.
			if config.NeedsInstall() {
				envPath, _ := config.ResolveConfigPath()
				install.RunInstallServer(install.DefaultInstallAddr, envPath, frontend)
			}
			bootstrap.InitApp()
			printBanner()
			if err := HttpServerStart(frontend); err != nil {
				panic(err)
			}
			if selfupdate.RestartRequested() {
				os.Exit(selfupdate.RestartExitCode())
			}
		},
	})
	return httpCmd
}

func HttpServerStart(frontend http.FileSystem) error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(quit)

	e := echo.New()
	e.HideBanner = true
	e.HTTPErrorHandler = luluHttp.HandleError

	MiddlewareRegister(e)
	scanner, stopTasks := task.Start()
	defer stopTasks()
	route.RegisterRouteWithScanner(e, scanner)
	e.Static(config.StaticPath, config.StaticFilePath)

	// Revalidate assets when the embedded frontend changes.
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path := c.Request().URL.Path
			if strings.HasPrefix(path, "/assets/") || path == "/version-check.js" || path == "/sw.js" || path == "/registerSW.js" {
				c.Response().Header().Set("Cache-Control", "no-cache")
			}
			return next(c)
		}
	})
	e.Use(echoMiddleware.StaticWithConfig(echoMiddleware.StaticConfig{
		Skipper: func(c echo.Context) bool {
			path := c.Request().URL.Path
			if path == "/install" || strings.HasPrefix(path, "/install/") {
				// The install wizard is only served by install.RunInstallServer
				// before bootstrap. Once main server starts, block /install.
				return true
			}
			return luluHttp.ShouldSkipSPAFallback(path)
		},
		HTML5:      true,
		Index:      "index.html",
		Filesystem: frontend,
	}))

	httpListen := viper.GetString("http_listen")
	listener, err := net.Listen("tcp", httpListen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", httpListen, err)
	}
	e.Listener = listener
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- e.Start(httpListen)
	}()
	if err := waitForHTTPReady(listener.Addr()); err != nil {
		return err
	}
	selfupdate.MarkHealthy()

	select {
	case <-quit:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := e.Shutdown(ctx); err != nil {
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		return nil
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("HTTP server stopped: %w", err)
	}
}

func waitForHTTPReady(addr net.Addr) error {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return fmt.Errorf("parse HTTP listener address: %w", err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	probeURL := "http://" + net.JoinHostPort(host, port) + "/"
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		response, err := client.Get(probeURL)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
				return nil
			}
			err = fmt.Errorf("HTTP readiness probe returned %s", response.Status)
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("HTTP server did not become ready: %w", lastErr)
}

func MiddlewareRegister(e *echo.Echo) {
	if config.HTTPAccessLog {
		e.Use(echoMiddleware.Logger())
	}
	e.Use(middleware.RequestUUID())
}
