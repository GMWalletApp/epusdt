package controller

import (
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/http"
	"github.com/gookit/validate"
	"github.com/gookit/validate/locales/zhcn"
	"github.com/gookit/validate/locales/zhtw"
	"github.com/labstack/echo/v4"
)

var Ctrl = &BaseController{}

type BaseController struct {
	http.Resp
}

func (c *BaseController) ValidateStruct(ctx echo.Context, i interface{}, scene ...string) error {
	v := validate.Struct(i, scene...)
	if ctx.Request().Header.Get("locale") == "zh-tw" {
		zhtw.Register(v)
	} else {
		zhcn.Register(v)
	}

	if !v.Validate() {
		return constant.ParamsMarshalErr
	}
	return nil
}
