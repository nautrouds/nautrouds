package metrics

import (
	"context"
	"sync/atomic"
	"time"
)

type ownTimeKey struct{}

func NewOwnTimeContext(ctx context.Context) (context.Context, *atomic.Int64) {
	acc := new(atomic.Int64)
	return context.WithValue(ctx, ownTimeKey{}, acc), acc
}

func AddExternalDuration(ctx context.Context, d time.Duration) {
	if acc, ok := ctx.Value(ownTimeKey{}).(*atomic.Int64); ok {
		acc.Add(int64(d))
	}
}
