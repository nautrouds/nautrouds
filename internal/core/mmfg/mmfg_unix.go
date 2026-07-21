//go:build unix

package mmfg

import (
	"context"
	"net/http"

	"github.com/nautrouds/mmfg-http/go/mmfghttp"
)

type UnixHub struct {
	hub *mmfghttp.Hub
}

func NewHub() (Hub, error) {
	hub, err := mmfghttp.NewHub()
	if err != nil {
		return nil, err
	}

	return &UnixHub{
		hub: hub,
	}, nil
}

func (h *UnixHub) Enabled() bool {
	return true
}

func (h *UnixHub) Dial(nodeName string, socketPath string, controlSocketPath string) error {
	return h.hub.Dial(nodeName, socketPath, controlSocketPath)
}

func (h *UnixHub) Request(ctx context.Context, r *http.Request) (Request, error) {
	return h.hub.Request(ctx, r)
}
