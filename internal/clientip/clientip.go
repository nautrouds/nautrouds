package clientip

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func FromRequest(r *http.Request) string {
	values := r.Header.Values("X-Forwarded-For")
	if len(values) == 0 {
		return ""
	}

	value := values[len(values)-1]
	if i := strings.LastIndexByte(value, ','); i >= 0 {
		value = value[i+1:]
	}
	value = strings.TrimSpace(value)

	if addr, err := netip.ParseAddr(value); err == nil {
		return addr.String()
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		if addr, err := netip.ParseAddr(host); err == nil {
			return addr.String()
		}
	}
	return ""
}
