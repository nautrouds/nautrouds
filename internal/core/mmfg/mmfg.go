package mmfg

import (
	"context"
	"net/http"
	"net/url"
)

type Hub interface {
	Extension() string
	ApplyFullScan(baseDir string, byService map[string]map[string]struct{}) error
	ApplyServiceScan(baseDir string, serviceName string, discovered []string) error
	Request(ctx context.Context, r *http.Request) (Request, error)
}

type Request interface {
	Inject(req *http.Request) error
	Cookies() ([]*http.Cookie, error)
	SetCookie(name string, value string) error
	DeleteCookie(name string) error
	Method() (string, error)
	SetMethod(method string) error
	URL() (*url.URL, error)
	SetURL(rawURL string) error
	Header(key string) (string, error)
	CloneHeaders() (http.Header, error)
	UpdateHeader(key string, newValue ...string) error
	DeleteHeader(key string) error
	Next(nodeName string) (bool, error)
	Apply() error
	AcceptSelfResponse(w http.ResponseWriter) error
}
