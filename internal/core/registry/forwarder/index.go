package forwarder

import (
	"context"
	"errors"
	"maps"
	"nautrouds/internal/core/builtins/builtinsmware"
	"nautrouds/internal/core/metrics"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

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
		}
		http.Error(w, "Node Failure", http.StatusBadGateway)
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

func (f *Forwarder) ForwardMiddleware(w *builtinsmware.ResponseWriter, r *http.Request, path string) bool {
	if f.isFailed.Load() {
		w.Reply("Service Unavailable", http.StatusServiceUnavailable)
		return false
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
		w.Reply("Middleware Init Error", http.StatusInternalServerError)
		return false
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
		w.Reply("Middleware Error", http.StatusInternalServerError)
		return false
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		maps.Copy(w.Header(), response.Header)
		w.ReplyReader(response.Body, response.StatusCode)
		return false
	}

	for k, vv := range response.Header {
		r.Header.Del(k)
		for _, v := range vv {
			r.Header.Add(k, v)
		}
	}

	return true
}

func (f *Forwarder) Forward(w http.ResponseWriter, r *http.Request) {
	if f.isFailed.Load() {
		http.Error(w, "Service Unavailable (Node Failed)", http.StatusServiceUnavailable)
		return
	}
	f.wg.Add(1)
	defer f.wg.Done()

	f.reverseProxy.ServeHTTP(w, r)
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
