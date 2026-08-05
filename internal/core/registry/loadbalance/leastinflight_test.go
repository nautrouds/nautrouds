package loadbalance

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"nautrouds/internal/core/registry/forwarder"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLoad struct {
	name string
	load int64
}

func (f *fakeLoad) InFlightWeight() int64 { return f.load }

func loadsOf(items []*fakeLoad) []int64 {
	loads := make([]int64, len(items))
	for i, it := range items {
		loads[i] = it.InFlightWeight()
	}
	return loads
}

func TestSortByLoad(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		assert.Empty(t, SortByLoad[*fakeLoad](nil))
		assert.Empty(t, SortByLoad([]*fakeLoad{}))
	})

	t.Run("Single", func(t *testing.T) {
		f := &fakeLoad{name: "a", load: 3}
		result := SortByLoad([]*fakeLoad{f})
		require.Len(t, result, 1)
		assert.Same(t, f, result[0])
	})

	t.Run("AlreadySorted", func(t *testing.T) {
		in := []*fakeLoad{{name: "a", load: 0}, {name: "b", load: 1}, {name: "c", load: 2}}
		result := SortByLoad(in)
		assert.Equal(t, []int64{0, 1, 2}, loadsOf(result))
		for i := range in {
			assert.Same(t, in[i], result[i])
		}
	})

	t.Run("ReverseSorted", func(t *testing.T) {
		a, b, c := &fakeLoad{name: "a", load: 5}, &fakeLoad{name: "b", load: 3}, &fakeLoad{name: "c", load: 1}
		result := SortByLoad([]*fakeLoad{a, b, c})
		assert.Equal(t, []int64{1, 3, 5}, loadsOf(result))
		assert.Same(t, c, result[0])
		assert.Same(t, b, result[1])
		assert.Same(t, a, result[2])
	})

	t.Run("Ties", func(t *testing.T) {
		a := &fakeLoad{name: "a", load: 1}
		b := &fakeLoad{name: "b", load: 2}
		c := &fakeLoad{name: "c", load: 1}
		d := &fakeLoad{name: "d", load: 2}
		result := SortByLoad([]*fakeLoad{a, b, c, d})
		assert.Equal(t, []int64{1, 1, 2, 2}, loadsOf(result))
		// stable sort: ties keep original order
		assert.Same(t, a, result[0])
		assert.Same(t, c, result[1])
		assert.Same(t, b, result[2])
		assert.Same(t, d, result[3])
	})

	t.Run("Mixed", func(t *testing.T) {
		a := &fakeLoad{name: "a", load: 4}
		b := &fakeLoad{name: "b", load: 1}
		c := &fakeLoad{name: "c", load: 4}
		d := &fakeLoad{name: "d", load: 2}
		e := &fakeLoad{name: "e", load: 1}
		result := SortByLoad([]*fakeLoad{a, b, c, d, e})
		assert.Equal(t, []int64{1, 1, 2, 4, 4}, loadsOf(result))
		assert.Same(t, b, result[0])
		assert.Same(t, e, result[1])
		assert.Same(t, d, result[2])
		assert.Same(t, a, result[3])
		assert.Same(t, c, result[4])
	})

	t.Run("InputNotMutated", func(t *testing.T) {
		a, b, c := &fakeLoad{name: "a", load: 5}, &fakeLoad{name: "b", load: 3}, &fakeLoad{name: "c", load: 1}
		in := []*fakeLoad{a, b, c}
		SortByLoad(in)
		assert.Same(t, a, in[0])
		assert.Same(t, b, in[1])
		assert.Same(t, c, in[2])
	})
}

func TestLeastInFlight_GetEmptyReturnsNil(t *testing.T) {
	li := NewLeastInFlight(nil)
	assert.Nil(t, li.Get())
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
		want := fwds[(firstIdx+i)%len(fwds)]
		if got := li.Get()[0]; got != want {
			t.Fatalf("call %d: Get()[0] = %v, want %v", i, got, want)
		}
	}
}

func TestLeastInFlight_GetReturnsEachForwarderOnce(t *testing.T) {
	fwds := []*forwarder.Forwarder{
		makeForwarder("/svc/node-0.sock"),
		makeForwarder("/svc/node-1.sock"),
		makeForwarder("/svc/node-2.sock"),
	}
	li := NewLeastInFlight(fwds)

	result := li.Get()
	if len(result) != len(fwds) {
		t.Fatalf("len(result) = %d, want %d", len(result), len(fwds))
	}

	seen := make(map[*forwarder.Forwarder]bool, len(fwds))
	for _, fwd := range result {
		if seen[fwd] {
			t.Fatalf("forwarder %v returned more than once", fwd)
		}
		seen[fwd] = true
	}
}

func TestLeastInFlight_GetP2C_EmptyReturnsNil(t *testing.T) {
	li := NewLeastInFlight(nil)
	assert.Nil(t, li.GetP2C())
}

func TestLeastInFlight_GetP2C_SingleReturnsThatOne(t *testing.T) {
	fwd := makeForwarder("/svc/node-0.sock")
	li := NewLeastInFlight([]*forwarder.Forwarder{fwd})

	result := li.GetP2C()
	require.Len(t, result, 1)
	assert.Same(t, fwd, result[0])
}

func TestLeastInFlight_GetP2C_ReturnsEachForwarderOnce(t *testing.T) {
	fwds := []*forwarder.Forwarder{
		makeForwarder("/svc/node-0.sock"),
		makeForwarder("/svc/node-1.sock"),
		makeForwarder("/svc/node-2.sock"),
		makeForwarder("/svc/node-3.sock"),
	}
	li := NewLeastInFlight(fwds)

	for range 50 {
		result := li.GetP2C()
		if len(result) != len(fwds) {
			t.Fatalf("len(result) = %d, want %d", len(result), len(fwds))
		}
		seen := make(map[*forwarder.Forwarder]bool, len(fwds))
		for _, fwd := range result {
			if seen[fwd] {
				t.Fatalf("forwarder %v returned more than once", fwd)
			}
			seen[fwd] = true
		}
	}
}

func TestLeastInFlight_GetP2C_PrimaryNeverWorseThanSecondary(t *testing.T) {
	fwds := []*forwarder.Forwarder{
		makeForwarder("/svc/node-0.sock"),
		makeForwarder("/svc/node-1.sock"),
		makeForwarder("/svc/node-2.sock"),
		makeForwarder("/svc/node-3.sock"),
	}
	li := NewLeastInFlight(fwds)

	for range 50 {
		result := li.GetP2C()
		if result[0].InFlightWeight() > result[1].InFlightWeight() {
			t.Fatalf("primary load %d > secondary load %d", result[0].InFlightWeight(), result[1].InFlightWeight())
		}
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

	tmpDir, err := os.MkdirTemp("", "nautrouds-leastinflight-*")
	require.NoError(t, err)

	socketPath := filepath.Join(tmpDir, "node.sock")
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
			os.RemoveAll(tmpDir)
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
		result := li.GetP2C()
		if result[0] != idle {
			t.Fatalf("call %d: primary = %v, want idle forwarder", i, result[0])
		}
	}
}
