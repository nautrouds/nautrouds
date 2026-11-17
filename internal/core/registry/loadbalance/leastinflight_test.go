package loadbalance

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"nautrouds/internal/core/registry/forwarder"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLoad struct {
	load int64
}

func (f *fakeLoad) InFlightWeight() int64 { return f.load }

func TestSortByLoad(t *testing.T) {
	tests := []struct {
		name      string
		loads     []int64
		wantOrder []int
	}{
		{"Empty", nil, nil},
		{"Single", []int64{3}, []int{0}},
		{"AlreadySorted", []int64{0, 1, 2}, []int{0, 1, 2}},
		{"ReverseSorted", []int64{5, 3, 1}, []int{2, 1, 0}},
		{"TiesKeepOriginalOrder", []int64{1, 2, 1, 2}, []int{0, 2, 1, 3}},
		{"Mixed", []int64{4, 1, 4, 2, 1}, []int{1, 4, 3, 0, 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := make([]*fakeLoad, len(tt.loads))
			for i, load := range tt.loads {
				in[i] = &fakeLoad{load: load}
			}
			want := make([]*fakeLoad, len(tt.wantOrder))
			for i, idx := range tt.wantOrder {
				want[i] = in[idx]
			}
			original := append([]*fakeLoad(nil), in...)

			result := SortByLoad(in)

			require.Len(t, result, len(want))
			for i := range want {
				assert.Same(t, want[i], result[i], "position %d", i)
			}
			for i := range original {
				assert.Same(t, original[i], in[i], "input must not be mutated")
			}
		})
	}
}

func TestLeastInFlight_EmptyReturnsNil(t *testing.T) {
	li := NewLeastInFlight(nil)
	assert.Nil(t, li.Get())
	assert.Nil(t, li.GetP2C())
}

func TestLeastInFlight_GetRotatesTieBreakAmongEqualLoad(t *testing.T) {
	fwds := []*forwarder.Forwarder{
		makeForwarder("/svc/node-0.sock"),
		makeForwarder("/svc/node-1.sock"),
		makeForwarder("/svc/node-2.sock"),
	}
	li := NewLeastInFlight(fwds)

	first := li.Get()[0]
	firstIdx := 0
	for i, fwd := range fwds {
		if fwd == first {
			firstIdx = i
		}
	}

	for i := 1; i < 9; i++ {
		require.Same(t, fwds[(firstIdx+i)%len(fwds)], li.Get()[0], "call %d", i)
	}
}

func TestLeastInFlight_GetReturnsEachForwarderOnce(t *testing.T) {
	fwds := []*forwarder.Forwarder{
		makeForwarder("/svc/node-0.sock"),
		makeForwarder("/svc/node-1.sock"),
		makeForwarder("/svc/node-2.sock"),
	}
	assert.ElementsMatch(t, fwds, NewLeastInFlight(fwds).Get())
}

func TestLeastInFlight_GetP2C_SingleReturnsThatOne(t *testing.T) {
	fwd := makeForwarder("/svc/node-0.sock")
	result := NewLeastInFlight([]*forwarder.Forwarder{fwd}).GetP2C()

	require.Len(t, result, 1)
	assert.Same(t, fwd, result[0])
}

func TestLeastInFlight_GetP2C_ReturnsEachForwarderOncePrimaryFirst(t *testing.T) {
	fwds := []*forwarder.Forwarder{
		makeForwarder("/svc/node-0.sock"),
		makeForwarder("/svc/node-1.sock"),
		makeForwarder("/svc/node-2.sock"),
		makeForwarder("/svc/node-3.sock"),
	}
	li := NewLeastInFlight(fwds)

	for range 50 {
		result := li.GetP2C()
		require.ElementsMatch(t, fwds, result)
		require.LessOrEqual(t, result[0].InFlightWeight(), result[1].InFlightWeight())
	}
}

type blockingForwarder struct {
	fwd     *forwarder.Forwarder
	release chan struct{}
	wg      sync.WaitGroup
	cleanup func()
}

func newBlockingForwarder(t *testing.T) *blockingForwarder {
	t.Helper()

	socketPath := filepath.Join(t.TempDir(), "node.sock")
	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusOK)
				return
			}
			started <- struct{}{}
			<-release
			w.WriteHeader(http.StatusOK)
		}),
	}
	go server.Serve(l)

	bf := &blockingForwarder{
		fwd:     makeForwarder(socketPath),
		release: release,
		cleanup: func() {
			server.Shutdown(context.Background())
			l.Close()
		},
	}

	bf.wg.Go(func() {
		req := httptest.NewRequest("GET", "http://example.com/", nil)
		w := httptest.NewRecorder()
		bf.fwd.Forward(w, req)
	})

	select {
	case <-started:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for request to start")
	}

	return bf
}

func (bf *blockingForwarder) unblock() {
	close(bf.release)
	bf.wg.Wait()
	bf.cleanup()
}

func TestLeastInFlight_GetP2C_PrefersIdleOverBusy(t *testing.T) {
	busy := newBlockingForwarder(t)
	defer busy.unblock()

	idle := makeForwarder("/svc/idle.sock")

	li := NewLeastInFlight([]*forwarder.Forwarder{busy.fwd, idle})

	for i := range 20 {
		require.Same(t, idle, li.GetP2C()[0], "call %d", i)
	}
}
