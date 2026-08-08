package rtree

import (
	"bytes"
	"net/http"
	"slices"
)

const (
	MethodGet uint16 = 1 << iota
	MethodPost
	MethodPut
	MethodDelete
	MethodHead
	MethodConnect
	MethodOptions
	MethodTrace
	MethodPatch
	MethodAny uint16 = 0xFFF
)

const (
	WildcardGreedy byte = 0x01
	Wildcard       byte = 0x02
)

var HTTPMethodMap = map[string]uint16{
	http.MethodGet:     MethodGet,
	http.MethodPost:    MethodPost,
	http.MethodPut:     MethodPut,
	http.MethodDelete:  MethodDelete,
	http.MethodHead:    MethodHead,
	http.MethodConnect: MethodConnect,
	http.MethodOptions: MethodOptions,
	http.MethodTrace:   MethodTrace,
	http.MethodPatch:   MethodPatch,
}

// ReverseHost reverses the host part of the URL for better indexing (e.g., com.google.www)
func ReverseHost(url []byte) {
	slashIdx := bytes.IndexByte(url, '/')
	if slashIdx <= 1 {
		if slashIdx == -1 {
			slashIdx = len(url)
		}
		if slashIdx <= 1 {
			return
		}
	}

	host := url[:slashIdx]
	slices.Reverse(host)

	start := 0
	for i := 0; i <= len(host); i++ {
		if i == len(host) || host[i] == '.' {
			slices.Reverse(host[start:i])
			start = i + 1
		}
	}
}
