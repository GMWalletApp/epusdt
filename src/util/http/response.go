package http

import (
	"errors"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/page"
	"github.com/labstack/echo/v4"
	"net/http"
)

type Resp struct{}

type Response struct {
	StatusCode int         `json:"status_code"`
	Message    string      `json:"message"`
	Data       interface{} `json:"data"`
	RequestID  string      `json:"request_id"`
}

func (r *Resp) View(e echo.Context, code int, html string) error {
	return e.HTML(code, html)
}

// SucView 成功返回
func (r *Resp) SucView(e echo.Context, html string) error {
	return r.View(e, http.StatusOK, html)
}

func (r *Resp) Json(e echo.Context, code int, data interface{}) error {
	return e.JSON(code, data)
}

// SucJson 成功返回json
func (r *Resp) SucJson(e echo.Context, data interface{}, message ...string) error {
	rp := new(Response)
	rp.StatusCode = http.StatusOK
	if len(message) == 0 {
		rp.Message = "success"
	} else {
		for _, m := range message {
			rp.Message += "," + m
		}
	}
	rp.Data = data
	rp.RequestID = e.Request().Header.Get(echo.HeaderXRequestID)
	return r.Json(e, http.StatusOK, rp)
}

// SucJsonPage 分页封装返回json
func (r *Resp) SucJsonPage(e echo.Context, data interface{}, pagination page.Pagination, message ...string) error {
	type PageData struct {
		List       interface{}     `json:"list"`
		Pagination page.Pagination `json:"pagination"`
	}
	pageDate := PageData{
		List:       data,
		Pagination: pagination,
	}
	return r.SucJson(e, pageDate, message...)
}

// FailJson retains the v1 controller fallback for unclassified service errors.
func (r *Resp) FailJson(ctx echo.Context, err error) error {
	return writeError(ctx, err, constant.SystemErr.(*constant.RspError))
}

// HandleError formats errors returned by routes and middleware, including wrapped
// errors. A handler that already wrote its response owns its body and headers.
func HandleError(err error, ctx echo.Context) {
	if writeErr := writeError(ctx, err, constant.InternalServerErr.(*constant.RspError)); writeErr != nil {
		ctx.Logger().Error(writeErr)
	}
}

func writeError(ctx echo.Context, err error, fallback *constant.RspError) error {
	if ctx.Response().Committed {
		return nil
	}
	status := fallback.HttpStatus()
	body := Response{StatusCode: fallback.Code, Message: fallback.Msg,
		RequestID: ctx.Request().Header.Get(echo.HeaderXRequestID)}
	var framework *echo.HTTPError
	var business *constant.RspError
	switch {
	case errors.As(err, &framework) && framework != nil:
		status = framework.Code
		body.StatusCode = status
		body.Message = http.StatusText(status)
		if msg, ok := framework.Message.(string); ok && status < 500 {
			body.Message = msg
		}
	case errors.As(err, &business) && business != nil:
		status = business.HttpStatus()
		body.StatusCode, body.Message = business.Render()
	}
	return ctx.JSON(status, body)
}
