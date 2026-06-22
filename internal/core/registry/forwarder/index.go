package forwarder

import (
	"context"
	"errors"
	"maps"
	"nautrouds/internal/core/metrics"
	"nautrouds/internal/core/tempresp"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

type proxyErrorKey struct{}

var (
	ErrNodeUnavailable  = errors.New("Node Unavailable")
	ErrMiddlewareBlocked = errors.New("Middleware Blocked")
)

var forbiddenHeaders = map[string]bool{
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Transfer-Encoding":   true,
	"Connection":          true,
	"Server":              true,
}

type FailureForwarder struct {
	SocketPath string
	Error      error
}

type Forwarder struct {
	serviceName  string
	socketPath   string
	client       *http.Client
	reverseProxy *httputil.ReverseProxy
	onFailure    chan FailureForwarder
	wg           sync.WaitGroup
	isFailed     atomic.Bool
}

func New(serviceName, nodePath string, onFailure chan FailureForwarder) *Forwarder {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", nodePath)
		},
		MaxIdleConnsPerHost: 100,
		DisableCompression:  true,
		DisableKeepAlives:   false,
	}

	f := &Forwarder{
		serviceName: serviceName,
		socketPath:  nodePath,
		onFailure:   onFailure,
		client: &http.Client{
			Transport: transport,
			Timeout:   1 * time.Second,
		},
	}

	f.reverseProxy = createReverseProxy(serviceName, nodePath, transport, onFailure, &f.isFailed)
	go probeSocket(nodePath, onFailure, &f.isFailed)

	return f
}

func (f *Forwarder) Wait() {
	f.wg.Wait()
}

func createReverseProxy(serviceName, nodePath string, transport http.RoundTripper, onFailure chan FailureForwarder, isFailed *atomic.Bool) *httputil.ReverseProxy {
	target, _ := url.Parse("http://unix-socket")
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.Transport = transport

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		var opErr *net.OpError
		if errors.As(err, &opErr) {
			if isFailed.CompareAndSwap(false, true) {
				onFailure <- FailureForwarder{SocketPath: nodePath, Error: opErr.Err}
			}
			err = ErrNodeUnavailable
		}

		if errTarget, ok := r.Context().Value(proxyErrorKey{}).(*error); ok {
			*errTarget = err
		} else {
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
		}
	}

	// Track upstream duration
	originalDirector := rp.Director
	rp.Director = func(req *http.Request) {
		originalDirector(req)
		req.Header.Set("X-Nautrouds-Start-Time", time.Now().Format(time.RFC3339Nano))
	}

	rp.ModifyResponse = func(resp *http.Response) error {
		if startStr := resp.Request.Header.Get("X-Nautrouds-Start-Time"); startStr != "" {
			if start, err := time.Parse(time.RFC3339Nano, startStr); err == nil {
				duration := time.Since(start).Seconds()
				metrics.Global.UpstreamDuration.WithLabelValues(serviceName, nodePath).Observe(duration)
			}
		}
		return nil
	}

	return rp
}

// ForwardMiddleware sends a GET request to the middleware service.
// Returns nil if the middleware approved (204), ErrNodeUnavailable if the node
// is unreachable (caller should retry with another node), or ErrMiddlewareBlocked
// if the middleware intentionally blocked the request (response already written to w).
func (f *Forwarder) ForwardMiddleware(w *tempresp.ResponseWriter, r *http.Request, path string) error {
	if f.isFailed.Load() {
		return ErrNodeUnavailable
	}

	f.wg.Add(1)
	defer f.wg.Done()

	start := time.Now()
	defer func() {
		metrics.Global.UpstreamDuration.WithLabelValues(f.serviceName, f.socketPath).Observe(time.Since(start).Seconds())
	}()

	if len(path) > 0 && path[0] != '/' {
		path = "/" + path
	}
	request, err := http.NewRequestWithContext(r.Context(), "GET", "http://localhost"+path, nil)
	if err != nil {
		return ErrNodeUnavailable
	}

	request.Header = r.Header.Clone()
	request.Host = r.Host

	request.Header.Set("X-Real-IP", r.RemoteAddr)

	response, err := f.client.Do(request)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) {
			if f.isFailed.CompareAndSwap(false, true) {
				f.onFailure <- FailureForwarder{SocketPath: f.socketPath, Error: opErr.Err}
			}
		}
		return ErrNodeUnavailable
	}
	defer response.Body.Close()

	for key := range forbiddenHeaders {
		response.Header.Del(key)
	}

	if response.StatusCode == http.StatusNoContent {
		for key, values := range response.Header {
			for _, value := range values {
				r.Header.Add(key, value)
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}

	w.EnablePassthrough()
	maps.Copy(w.Header(), response.Header)
	w.ReplyReader(response.Body, response.StatusCode)
	return ErrMiddlewareBlocked
}

func (f *Forwarder) Forward(w http.ResponseWriter, r *http.Request) error {
	if f.isFailed.Load() {
		return ErrNodeUnavailable
	}
	f.wg.Add(1)
	defer f.wg.Done()

	var proxyErr error
	ctx := context.WithValue(r.Context(), proxyErrorKey{}, &proxyErr)

	reqClone := r.Clone(ctx)
	f.reverseProxy.ServeHTTP(w, reqClone)

	if proxyErr != nil {
		return proxyErr
	}

	return nil
}

func (f *Forwarder) TryReconnect() error {
	conn, err := net.DialTimeout("unix", f.socketPath, 1*time.Second)
	if err != nil {
		return err
	}
	conn.Close()

	if t, ok := f.client.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}

	f.isFailed.Store(false)
	return nil
}
