package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const maxResponseBody = 8 << 20

var responseBody = bytes.Repeat([]byte("x"), maxResponseBody)

var errMalformedRequest = errors.New("malformed request")

type requestSpec struct {
	bodySize   int
	delay      time.Duration
	closeAfter bool
}

func main() {
	socketPath := flag.String("socket", os.Getenv("BACKEND_SOCKET"), "unix socket path to listen on")
	defaultBodySize := flag.Int("default-body-size", 64, "response body size when the request has no size query")
	flag.Parse()

	if *socketPath == "" {
		log.Fatal("missing -socket or BACKEND_SOCKET")
	}
	if err := os.MkdirAll(filepath.Dir(*socketPath), 0o755); err != nil {
		log.Fatalf("create socket dir: %v", err)
	}
	os.Remove(*socketPath)

	listener, err := net.Listen("unix", *socketPath)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	os.Chmod(*socketPath, 0o666)
	log.Printf("listening on %s", *socketPath)

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		listener.Close()
		os.Remove(*socketPath)
		os.Exit(0)
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go serveConn(conn, *defaultBodySize)
	}
}

func serveConn(conn net.Conn, defaultBodySize int) {
	defer conn.Close()
	reader := bufio.NewReaderSize(conn, 16<<10)
	writer := bufio.NewWriterSize(conn, 16<<10)
	header := make([]byte, 0, 128)

	for {
		spec, err := readRequest(reader, defaultBodySize)
		if err != nil {
			return
		}
		if spec.delay > 0 {
			time.Sleep(spec.delay)
		}

		header = header[:0]
		header = append(header, "HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: "...)
		header = strconv.AppendInt(header, int64(spec.bodySize), 10)
		if spec.closeAfter {
			header = append(header, "\r\nConnection: close"...)
		}
		header = append(header, "\r\n\r\n"...)
		writer.Write(header)
		writer.Write(responseBody[:spec.bodySize])
		if reader.Buffered() == 0 || spec.closeAfter {
			if err := writer.Flush(); err != nil {
				return
			}
		}
		if spec.closeAfter {
			return
		}
	}
}

func readRequest(reader *bufio.Reader, defaultBodySize int) (requestSpec, error) {
	spec := requestSpec{bodySize: defaultBodySize}

	requestLine, err := readLine(reader)
	if err != nil {
		return spec, err
	}
	fields := bytes.Fields(requestLine)
	if len(fields) != 3 || !bytes.HasPrefix(fields[2], []byte("HTTP/1.")) {
		return spec, errMalformedRequest
	}
	spec.closeAfter = bytes.Equal(fields[2], []byte("HTTP/1.0"))
	if err := applyQuery(&spec, fields[1]); err != nil {
		return spec, err
	}

	contentLength := int64(0)
	chunked := false
	for {
		line, err := readLine(reader)
		if err != nil {
			return spec, err
		}
		if len(line) == 0 {
			break
		}
		name, value, ok := bytes.Cut(line, []byte(":"))
		if !ok {
			return spec, errMalformedRequest
		}
		value = bytes.TrimSpace(value)
		switch {
		case bytes.EqualFold(name, []byte("Content-Length")):
			contentLength, err = strconv.ParseInt(string(value), 10, 64)
			if err != nil || contentLength < 0 {
				return spec, errMalformedRequest
			}
		case bytes.EqualFold(name, []byte("Transfer-Encoding")):
			chunked = bytes.Contains(bytes.ToLower(value), []byte("chunked"))
		case bytes.EqualFold(name, []byte("Connection")):
			if bytes.EqualFold(value, []byte("close")) {
				spec.closeAfter = true
			}
		}
	}

	if chunked {
		return spec, discardChunkedBody(reader)
	}
	if contentLength > 0 {
		if _, err := reader.Discard(int(contentLength)); err != nil {
			return spec, err
		}
	}
	return spec, nil
}

func applyQuery(spec *requestSpec, target []byte) error {
	_, rawQuery, found := bytes.Cut(target, []byte("?"))
	if !found {
		return nil
	}
	query, err := url.ParseQuery(string(rawQuery))
	if err != nil {
		return errMalformedRequest
	}
	if size := query.Get("size"); size != "" {
		n, err := strconv.Atoi(size)
		if err != nil || n < 0 || n > maxResponseBody {
			return errMalformedRequest
		}
		spec.bodySize = n
	}
	if delay := query.Get("delay"); delay != "" {
		d, err := time.ParseDuration(delay)
		if err != nil || d < 0 {
			return errMalformedRequest
		}
		spec.delay = d
	}
	return nil
}

func discardChunkedBody(reader *bufio.Reader) error {
	for {
		line, err := readLine(reader)
		if err != nil {
			return err
		}
		sizeField, _, _ := bytes.Cut(line, []byte(";"))
		size, err := strconv.ParseInt(string(bytes.TrimSpace(sizeField)), 16, 64)
		if err != nil || size < 0 {
			return errMalformedRequest
		}
		if size == 0 {
			for {
				trailer, err := readLine(reader)
				if err != nil {
					return err
				}
				if len(trailer) == 0 {
					return nil
				}
			}
		}
		if _, err := reader.Discard(int(size) + 2); err != nil {
			return err
		}
	}
}

func readLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) {
			return nil, fmt.Errorf("%w: line too long", errMalformedRequest)
		}
		if errors.Is(err, io.EOF) && len(line) > 0 {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return bytes.TrimRight(line, "\r\n"), nil
}
