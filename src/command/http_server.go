package command

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/GMWalletApp/epusdt/bootstrap"
	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/install"
	"github.com/GMWalletApp/epusdt/middleware"
	"github.com/GMWalletApp/epusdt/route"
	"github.com/GMWalletApp/epusdt/task"
	"github.com/GMWalletApp/epusdt/util/constant"
	luluHttp "github.com/GMWalletApp/epusdt/util/http"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var httpCmd = &cobra.Command{
	Use:   "http",
	Short: "http service",
	Long:  "http service commands",
	Run: func(cmd *cobra.Command, args []string) {
	},
}

func init() {
	httpCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
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
			install.RunInstallServer(install.DefaultInstallAddr, envPath)
		}
		bootstrap.InitApp()
		printBanner()
		HttpServerStart()
	},
}

func HttpServerStart() {
	var err error
	e := echo.New()
	e.HideBanner = true
	e.HTTPErrorHandler = customHTTPErrorHandler

	MiddlewareRegister(e)
	scanner, stopTasks := task.Start()
	defer stopTasks()
	route.RegisterRouteWithScanner(e, scanner)
	e.Static(config.StaticPath, config.StaticFilePath)

	// Resolve www/ relative to the executable so SPA routes work regardless
	// of the working directory. main.go extracts www/ next to the binary.
	wwwRoot := "./www"
	if exePath, err := os.Executable(); err == nil {
		if exePath, err = filepath.EvalSymlinks(exePath); err == nil {
			wwwRoot = filepath.Join(filepath.Dir(exePath), "www")
		}
	}
	// Revalidate assets when the embedded frontend changes.
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if strings.HasPrefix(c.Request().URL.Path, "/assets/") {
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
		HTML5: true,
		Index: "index.html",
		Root:  wwwRoot,
	}))

	httpListen := viper.GetString("http_listen")
	go func() {
		if err = e.Start(httpListen); err != http.ErrServerClosed {
			log.Sugar.Error(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(quit)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = e.Shutdown(ctx); err != nil {
		e.Logger.Fatal(err)
	}
}

func MiddlewareRegister(e *echo.Echo) {
	if config.HTTPAccessLog {
		e.Use(echoMiddleware.Logger())
	}
	e.Use(middleware.RequestUUID())
}

func customHTTPErrorHandler(err error, e echo.Context) {
	code := http.StatusInternalServerError
	msg := "server error"
	resp := &luluHttp.Response{
		StatusCode: code,
		Message:    msg,
		RequestID:  e.Request().Header.Get(echo.HeaderXRequestID),
	}
	// echo.HTTPError carries a real HTTP status (401 for auth failures,
	// 404 for missing routes, etc.). Propagate it instead of flattening
	// everything to 200 — clients rely on the status code.
	if he, ok := err.(*echo.HTTPError); ok {
		resp.StatusCode = he.Code
		if s, ok := he.Message.(string); ok {
			resp.Message = s
		} else if he.Message != nil {
			resp.Message = http.StatusText(he.Code)
		}
		_ = e.JSON(he.Code, resp)
		return
	}
	// Internal RspError: propagate Code as both the JSON status_code and
	// the real HTTP status when it maps to one (400/401/...); business
	// codes (>=1000) map to HTTP 400 so clients get a proper 4xx while
	// still reading the granular code from the body.
	if he, ok := err.(*constant.RspError); ok {
		resp.StatusCode = he.Code
		resp.Message = he.Msg
		_ = e.JSON(he.HttpStatus(), resp)
		return
	}
	_ = e.JSON(http.StatusInternalServerError, resp)
}
