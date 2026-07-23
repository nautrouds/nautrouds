package forwarder

import (
	"net/http"
	"sync/atomic"
)

// dualTransport is the single h1/h2 selection point shared by
// Forwarder.Forward and Forwarder.ForwardMiddleware.
type dualTransport struct {
	useHTTP2 *atomic.Bool
	h1       *http.Transport
	h2       *http.Transport
}

func (t *dualTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.useHTTP2.Load() {
		return t.h2.RoundTrip(req)
	}
	return t.h1.RoundTrip(req)
}

func (t *dualTransport) CloseIdleConnections() {
	t.h1.CloseIdleConnections()
	t.h2.CloseIdleConnections()
}
