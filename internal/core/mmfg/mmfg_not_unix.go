//go:build !unix

package mmfg

import (
	"context"
	"fmt"
	"net/http"
)

type NotUnixHub struct{}

func NewHub() (Hub, error) {
	return &NotUnixHub{}, nil
}

func (NotUnixHub) Enabled() bool {
	return false
}

func (NotUnixHub) Dial(nodeName string, socketPath string, controlSocketPath string) error {
	return fmt.Errorf("mmfg: unsupported on non-unix platforms")
}

func (NotUnixHub) Request(_ context.Context, _ *http.Request) (Request, error) {
	return nil, fmt.Errorf("mmfg: unsupported on non-unix platforms")
}
