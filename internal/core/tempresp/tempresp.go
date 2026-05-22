package tempresp

import (
	"bytes"
	"io"
	"net/http"
	"sync"
)

var _ http.ResponseWriter = (*ResponseWriter)(nil)
var _ io.Writer = (*ResponseWriter)(nil)

var Pool = sync.Pool{
	New: func() any {
		return newTempResponseWriter()
	},
}

// Response is a reusable, efficient response capture for middleware.
// It supports both internal buffering and direct io.ReadCloser streaming.
type ResponseWriter struct {
	downstream    http.ResponseWriter
	header        http.Header
	status        int
	body          *bytes.Buffer
	isPassthrough bool
}

func newTempResponseWriter() *ResponseWriter {
	return &ResponseWriter{
		header:        make(http.Header),
		body:          new(bytes.Buffer),
		status:        http.StatusOK,
		downstream:    nil,
		isPassthrough: false,
	}
}

func (t *ResponseWriter) EnablePassthrough() {
	t.isPassthrough = true
}

func (t *ResponseWriter) IsPassthrough() bool {
	return t.isPassthrough
}

func (t *ResponseWriter) Header() http.Header {
	if t.isPassthrough && t.downstream != nil {
		return t.downstream.Header()
	}
	return t.header
}

func (t *ResponseWriter) WriteHeader(code int) {
	if t.isPassthrough && t.downstream != nil {
		t.downstream.WriteHeader(code)
		return
	}
	t.status = code
}

func (t *ResponseWriter) Write(p []byte) (int, error) {
	if t.isPassthrough && t.downstream != nil {
		return t.downstream.Write(p)
	}
	return t.body.Write(p)
}

func (t *ResponseWriter) ReplyReader(r io.Reader, code int) {
	if t.isPassthrough && t.downstream != nil {
		t.downstream.WriteHeader(code)
		io.Copy(t.downstream, r)
		return
	}
	t.body.Reset()
	t.status = code
	io.Copy(t.body, r)
}

func (t *ResponseWriter) GetCode() int {
	return t.status
}

func (t *ResponseWriter) Reset() {
	for k := range t.header {
		delete(t.header, k)
	}
	t.body.Reset()
	t.status = http.StatusOK
	t.downstream = nil
	t.isPassthrough = false
}

func (t *ResponseWriter) Setup(w http.ResponseWriter) {
	t.Reset()
	t.downstream = w
}

func (t *ResponseWriter) Reply(msg string, code int) {
	if t.isPassthrough && t.downstream != nil {
		t.downstream.WriteHeader(code)
		t.downstream.Write([]byte(msg))
		return
	}
	t.body.Reset()
	t.body.WriteString(msg)
	t.status = code
}

func (t *ResponseWriter) WriteTo(w http.ResponseWriter) error {
	if t.isPassthrough {
		return nil
	}

	for k, vv := range t.header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(t.status)
	t.body.WriteTo(w)
	return nil
}

func (t *ResponseWriter) Commit() error {
	if t.downstream == nil && t.isPassthrough {
		return nil
	}

	header := t.downstream.Header()
	for k, vv := range t.header {
		for _, v := range vv {
			header.Add(k, v)
		}
	}

	t.downstream.WriteHeader(t.status)
	_, err := t.body.WriteTo(t.downstream)
	return err
}
