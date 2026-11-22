package controller

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/labstack/echo/v4"
)

func TestValidateStructConcurrentLocales(t *testing.T) {
	type params struct {
		Name string `validate:"required"`
	}
	server := echo.New()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, locale := range []string{"zh", "zh-tw", ""} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set("locale", locale)
			ctx := server.NewContext(req, httptest.NewRecorder())
			<-start
			for i := 0; i < 32; i++ {
				if err := Ctrl.ValidateStruct(ctx, &params{}); err != constant.ParamsMarshalErr {
					t.Errorf("locale %q invalid params: got %v", locale, err)
					return
				}
				if err := Ctrl.ValidateStruct(ctx, &params{Name: "ok"}); err != nil {
					t.Errorf("locale %q valid params: got %v", locale, err)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
}
