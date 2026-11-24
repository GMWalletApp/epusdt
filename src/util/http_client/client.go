package http_client

import (
	"net/http"
	"time"

	"github.com/GMWalletApp/epusdt/util/security"
	"github.com/go-resty/resty/v2"
)

// ClientFactory is overridden in tests to stub outbound HTTP calls.
var ClientFactory = resty.New

var CallbackClientFactory = newCallbackClient

// GetHttpClient 获取请求客户端
func GetHttpClient(proxys ...string) *resty.Client {
	client := ClientFactory()
	// 如果有代理
	if len(proxys) > 0 {
		proxy := proxys[0]
		client.SetProxy(proxy)
	}
	client.SetTimeout(time.Second * 10)
	return client
}

func newCallbackClient() *resty.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = security.DialPublicContext
	transport.DialTLSContext = nil
	return resty.New().SetTransport(transport)
}

func GetCallbackHttpClient() *resty.Client {
	return CallbackClientFactory().SetTimeout(10 * time.Second).SetRedirectPolicy(resty.NoRedirectPolicy())
}
