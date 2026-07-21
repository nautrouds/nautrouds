//go:build !unix

package mmfg

import (
	"context"
	"fmt"
	"net/http"
)

const IsAvailable = false

type NotUnixHub struct{}

func NewHub() (Hub, error) {
	return &NotUnixHub{}, nil
}

func (NotUnixHub) Extension() string {
	return ".mmfg"
}

func (NotUnixHub) ApplyFullScan(baseDir string, byService map[string]map[string]struct{}) error {
	return nil
}

func (NotUnixHub) ApplyServiceScan(baseDir string, serviceName string, discovered []string) error {
	return nil
}

func (NotUnixHub) Request(_ context.Context, _ *http.Request) (Request, error) {
	return nil, fmt.Errorf("mmfg: unsupported on non-unix platforms")
}
