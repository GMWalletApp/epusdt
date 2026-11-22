package admin

import (
	"context"
	"errors"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/selfupdate"
	"github.com/GMWalletApp/epusdt/internal/version"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/labstack/echo/v4"
)

type releaseChecker interface {
	CheckLatestRelease(context.Context, string, string) (*version.CheckResult, error)
	FetchLatestRelease(context.Context, string) (*version.Release, error)
}

var systemReleaseChecker releaseChecker = version.NewChecker()
var systemUpdateManager = selfupdate.NewManager(func(ctx context.Context) (*version.Release, error) {
	return systemReleaseChecker.FetchLatestRelease(ctx, config.GetUpdateRepository())
}, config.GetAppVersion)

type CurrentVersionResult struct {
	CurrentVersion string `json:"current_version"`
	TargetVersion  string `json:"target_version"`
	Confirmed      bool   `json:"confirmed"`
}

// GetCurrentSystemVersion reports the running binary version without making
// any external requests. A target is confirmed when the running version has
// reached or passed it.
// @Summary      Confirm the running system version
// @Tags         Admin System
// @Security     AdminJWT
// @Produce      json
// @Param        target_version query string false "Version expected after update"
// @Success      200 {object} response.ApiResponse{data=CurrentVersionResult}
// @Router       /admin/api/v1/system/version/current [get]
func (c *BaseAdminController) GetCurrentSystemVersion(ctx echo.Context) error {
	ctx.Response().Header().Set("Cache-Control", "no-store")
	current := config.GetAppVersion()
	target := ctx.QueryParam("target_version")
	confirmed := target == current
	if target != "" && !confirmed {
		newer, err := version.IsNewerVersion(current, target)
		confirmed = err == nil && newer
	}
	return c.SucJson(ctx, CurrentVersionResult{
		CurrentVersion: current,
		TargetVersion:  target,
		Confirmed:      confirmed,
	})
}

// CheckSystemUpdate checks the server-configured GitHub repository. The
// browser cannot provide an owner, repository, or URL, so this endpoint cannot
// be used as an arbitrary outbound request proxy.
// @Summary      Check for a stable release
// @Description  Compare the running build version with the latest stable GitHub Release.
// @Tags         Admin System
// @Security     AdminJWT
// @Produce      json
// @Success      200 {object} response.ApiResponse{data=version.CheckResult}
// @Failure      401 {object} response.ApiResponse
// @Failure      404 {object} response.ApiResponse
// @Failure      429 {object} response.ApiResponse
// @Failure      502 {object} response.ApiResponse
// @Failure      504 {object} response.ApiResponse
// @Router       /admin/api/v1/system/version/check [get]
func (c *BaseAdminController) CheckSystemUpdate(ctx echo.Context) error {
	ctx.Response().Header().Set("Cache-Control", "no-store")
	requestContext, cancel := context.WithTimeout(ctx.Request().Context(), 12*time.Second)
	defer cancel()

	result, err := systemReleaseChecker.CheckLatestRelease(requestContext, config.GetAppVersion(), config.GetUpdateRepository())
	if err == nil {
		return c.SucJson(ctx, result)
	}
	switch {
	case errors.Is(err, version.ErrRateLimited):
		return c.FailJson(ctx, constant.UpdateRateLimitedErr)
	case errors.Is(err, version.ErrNoRelease):
		return c.FailJson(ctx, constant.UpdateNoReleaseErr)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(requestContext.Err(), context.DeadlineExceeded):
		return c.FailJson(ctx, constant.UpdateCheckTimeoutErr)
	default:
		return c.FailJson(ctx, constant.UpdateCheckFailedErr)
	}
}

// @Summary      Get update capability
// @Tags         Admin System
// @Security     AdminJWT
// @Produce      json
// @Success      200 {object} response.ApiResponse{data=selfupdate.Capability}
// @Router       /admin/api/v1/system/update/capability [get]
func (c *BaseAdminController) GetUpdateCapability(ctx echo.Context) error {
	ctx.Response().Header().Set("Cache-Control", "no-store")
	capability := systemUpdateManager.Capability()
	if capability.CanUpdate {
		requestContext, cancel := context.WithTimeout(ctx.Request().Context(), 12*time.Second)
		release, err := systemReleaseChecker.FetchLatestRelease(requestContext, config.GetUpdateRepository())
		cancel()
		if err == nil {
			capability = systemUpdateManager.CapabilityForRelease(release)
		}
	}
	return c.SucJson(ctx, capability)
}

// @Summary      Get update status
// @Tags         Admin System
// @Security     AdminJWT
// @Produce      json
// @Success      200 {object} response.ApiResponse{data=selfupdate.State}
// @Router       /admin/api/v1/system/update/status [get]
func (c *BaseAdminController) GetUpdateStatus(ctx echo.Context) error {
	ctx.Response().Header().Set("Cache-Control", "no-store")
	return c.SucJson(ctx, systemUpdateManager.Status())
}

// @Summary      Start system update
// @Tags         Admin System
// @Security     AdminJWT
// @Produce      json
// @Success      200 {object} response.ApiResponse{data=selfupdate.State}
// @Router       /admin/api/v1/system/update/start [post]
func (c *BaseAdminController) StartSystemUpdate(ctx echo.Context) error {
	ctx.Response().Header().Set("Cache-Control", "no-store")
	requestContext, cancel := context.WithTimeout(ctx.Request().Context(), 15*time.Second)
	defer cancel()
	if err := systemUpdateManager.Start(requestContext); err != nil {
		switch {
		case errors.Is(err, selfupdate.ErrUpdateInProgress):
			return c.FailJson(ctx, constant.UpdateInProgressErr)
		case errors.Is(err, selfupdate.ErrNotSupported), errors.Is(err, selfupdate.ErrNoUpdate):
			return c.FailJson(ctx, constant.UpdateNotSupportedErr)
		case errors.Is(err, context.DeadlineExceeded):
			return c.FailJson(ctx, constant.UpdateCheckTimeoutErr)
		default:
			return c.FailJson(ctx, constant.UpdateFailedErr)
		}
	}
	return c.SucJson(ctx, systemUpdateManager.Status())
}
