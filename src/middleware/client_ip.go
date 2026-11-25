package middleware

import (
	"fmt"
	"net"
	"strings"

	"github.com/labstack/echo/v4"
)

func NewClientIPExtractor(trustedProxies string) (echo.IPExtractor, error) {
	var options []echo.TrustOption
	for _, item := range strings.Split(trustedProxies, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		_, network, err := net.ParseCIDR(item)
		if err != nil {
			ip := net.ParseIP(item)
			if ip == nil {
				return nil, fmt.Errorf("invalid trusted proxy %q", item)
			}
			bits := 128
			if ip.To4() != nil {
				ip = ip.To4()
				bits = 32
			}
			network = &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
		}
		options = append(options, echo.TrustIPRange(network))
	}
	if len(options) == 0 {
		return echo.ExtractIPDirect(), nil
	}
	return echo.ExtractIPFromXFFHeader(options...), nil
}
