package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type options struct {
	target      string
	path        string
	method      string
	requestBody int
	conns       int
	duration    time.Duration
	warmup      time.Duration
	timeout     time.Duration
	rate        float64
	closeEach   bool
	waitStdin   bool
}

type Result struct {
	Requests      int64   `json:"requests"`
	Errors        int64   `json:"errors"`
	Non2xx        int64   `json:"non2xx"`
	BodyBytesAvg  float64 `json:"bodyBytesAvg"`
	WallSec       float64 `json:"wallSec"`
	RPS           float64 `json:"rps"`
	TargetRPS     float64 `json:"targetRps"`
	MeanUs        float64 `json:"meanUs"`
	P50Us         float64 `json:"p50Us"`
	P90Us         float64 `json:"p90Us"`
	P99Us         float64 `json:"p99Us"`
	P999Us        float64 `json:"p999Us"`
	MaxUs         float64 `json:"maxUs"`
	ClientCPUSec  float64 `json:"clientCpuSec"`
	ClientCores   int     `json:"clientCores"`
	ClientUtil    float64 `json:"clientUtil"`
	DispatchP50Us float64 `json:"dispatchLagP50Us"`
	DispatchP99Us float64 `json:"dispatchLagP99Us"`
}

type worker struct {
	dial      func() (net.Conn, error)
	request   []byte
	closeEach bool
	timeout   time.Duration

	conn   net.Conn
	reader *bufio.Reader

	latencies []int64
	bodyBytes int64
	errors    int64
	non2xx    int64
}

var errMalformedResponse = errors.New("malformed response")

func main() {
	var opts options
	flag.StringVar(&opts.target, "target", "", "unix:///path/to.sock, tcp://host:port or tls://host:port")
	flag.StringVar(&opts.path, "path", "/", "request target, including query string")
	flag.StringVar(&opts.method, "method", "GET", "HTTP method")
	flag.IntVar(&opts.requestBody, "request-body", 0, "request body size in bytes")
	flag.IntVar(&opts.conns, "conns", 64, "number of concurrent connections")
	flag.DurationVar(&opts.duration, "duration", 10*time.Second, "measurement duration")
	flag.DurationVar(&opts.warmup, "warmup", 2*time.Second, "warmup duration before measurement")
	flag.DurationVar(&opts.timeout, "timeout", 10*time.Second, "per-request I/O timeout")
	flag.Float64Var(&opts.rate, "rate", 0, "open-loop target req/s across all connections (0 = closed-loop)")
	flag.BoolVar(&opts.closeEach, "close", false, "open a new connection for every request")
	flag.BoolVar(&opts.waitStdin, "wait-stdin", false, "after warmup, print WARM and wait for a line on stdin before measuring")
	flag.Parse()

	if opts.conns < 1 {
		log.Fatal("-conns must be >= 1")
	}
	dial, host, err := newDialer(opts.target)
	if err != nil {
		log.Fatal(err)
	}
	request := buildRequest(opts, host)
	clientCores := runtime.GOMAXPROCS(0)
	if opts.rate > 0 {
		// The dispatcher blocks in nanosleep on its own thread and needs a free P the moment
		// it wakes; if every P is busy running workers it waits for a scheduler slice instead.
		runtime.GOMAXPROCS(clientCores + 1)
	}

	workers := make([]*worker, opts.conns)
	for i := range workers {
		workers[i] = &worker{dial: dial, request: request, closeEach: opts.closeEach, timeout: opts.timeout}
	}

	if opts.warmup > 0 {
		runPhase(workers, opts.warmup, opts.rate)
		for _, w := range workers {
			w.latencies = w.latencies[:0]
			w.errors, w.non2xx, w.bodyBytes = 0, 0, 0
		}
	}
	if opts.waitStdin {
		fmt.Println("WARM")
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
			log.Fatalf("waiting for stdin: %v", err)
		}
	}

	cpuBefore := processCPUTime()
	wall, dispatchLags := runPhase(workers, opts.duration, opts.rate)
	cpuUsed := processCPUTime() - cpuBefore

	for _, w := range workers {
		if w.conn != nil {
			w.conn.Close()
		}
	}

	result := summarize(workers, wall, cpuUsed, opts.rate, dispatchLags, clientCores)
	encoded, _ := json.Marshal(result)
	fmt.Printf("RESULT %s\n", encoded)
}

func newDialer(target string) (func() (net.Conn, error), string, error) {
	scheme, address, ok := strings.Cut(target, "://")
	if !ok {
		return nil, "", fmt.Errorf("invalid -target %q", target)
	}
	switch scheme {
	case "unix":
		return func() (net.Conn, error) { return net.Dial("unix", address) }, "localhost", nil
	case "tcp":
		return func() (net.Conn, error) { return net.Dial("tcp", address) }, address, nil
	case "tls":
		config := &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"http/1.1"},
			ClientSessionCache: tls.NewLRUClientSessionCache(1024),
		}
		return func() (net.Conn, error) { return tls.Dial("tcp", address, config) }, address, nil
	default:
		return nil, "", fmt.Errorf("unsupported -target scheme %q", scheme)
	}
}

func buildRequest(opts options, host string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: nautrouds-bench\r\n", opts.method, opts.path, host)
	if opts.requestBody > 0 {
		fmt.Fprintf(&b, "Content-Type: application/octet-stream\r\nContent-Length: %d\r\n", opts.requestBody)
	}
	if opts.closeEach {
		b.WriteString("Connection: close\r\n")
	}
	b.WriteString("\r\n")
	b.Write(bytes.Repeat([]byte("y"), opts.requestBody))
	return b.Bytes()
}

func runPhase(workers []*worker, duration time.Duration, rate float64) (time.Duration, []int64) {
	start := time.Now()
	end := start.Add(duration)
	if rate > 0 {
		lags := runOpenLoop(workers, start, end, rate)
		return time.Since(start), lags
	}
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Go(func() { w.runClosedLoop(end) })
	}
	wg.Wait()
	return time.Since(start), nil
}

func (w *worker) runClosedLoop(end time.Time) {
	for {
		sent := time.Now()
		if !sent.Before(end) {
			return
		}
		if w.roundTrip() {
			w.latencies = append(w.latencies, int64(time.Since(sent)))
		}
	}
}

// Latency is measured from the scheduled send time rather than the actual one, so a
// stalled response also charges the requests queued behind it (coordinated omission).
func runOpenLoop(workers []*worker, start, end time.Time, rate float64) []int64 {
	total := int(rate * end.Sub(start).Seconds())
	schedule := make(chan time.Time, total+1)
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Go(func() {
			for intended := range schedule {
				if w.roundTrip() {
					w.latencies = append(w.latencies, int64(time.Since(intended)))
				}
			}
		})
	}
	lags := dispatch(schedule, start, total, rate)
	close(schedule)
	wg.Wait()
	return lags
}

// Go timers are driven by the netpoller with millisecond granularity, so time.Sleep
// for a sub-millisecond gap often oversleeps by up to 1 ms. That error would be charged
// to every request as latency, so the dispatcher sleeps on its own OS thread instead.
func dispatch(schedule chan<- time.Time, start time.Time, total int, rate float64) []int64 {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	tightenTimerSlack()

	lags := make([]int64, 0, total)
	for i := range total {
		intended := start.Add(time.Duration(float64(i) * float64(time.Second) / rate))
		if wait := time.Until(intended); wait > 0 {
			preciseSleep(wait)
		}
		lags = append(lags, int64(time.Since(intended)))
		schedule <- intended
	}
	return lags
}

func (w *worker) roundTrip() bool {
	ok, err := w.exchange()
	if err != nil {
		w.errors++
		w.resetConn()
		time.Sleep(time.Millisecond)
		return false
	}
	return ok
}

func (w *worker) exchange() (bool, error) {
	if w.conn == nil {
		conn, err := w.dial()
		if err != nil {
			return false, err
		}
		w.conn = conn
		if w.reader == nil {
			w.reader = bufio.NewReaderSize(conn, 64<<10)
		} else {
			w.reader.Reset(conn)
		}
	}

	w.conn.SetDeadline(time.Now().Add(w.timeout))
	if _, err := w.conn.Write(w.request); err != nil {
		return false, err
	}
	status, bodyBytes, serverClose, err := readResponse(w.reader)
	if err != nil {
		return false, err
	}
	if w.closeEach || serverClose {
		w.resetConn()
	}
	if status < 200 || status >= 300 {
		w.non2xx++
		return false, nil
	}
	w.bodyBytes += bodyBytes
	return true, nil
}

func (w *worker) resetConn() {
	if w.conn != nil {
		w.conn.Close()
		w.conn = nil
	}
}

func readResponse(reader *bufio.Reader) (status int, bodyBytes int64, serverClose bool, err error) {
	statusLine, err := readLine(reader)
	if err != nil {
		return 0, 0, false, err
	}
	if len(statusLine) < 12 || !bytes.HasPrefix(statusLine, []byte("HTTP/1.")) {
		return 0, 0, false, errMalformedResponse
	}
	status, err = strconv.Atoi(string(statusLine[9:12]))
	if err != nil {
		return 0, 0, false, errMalformedResponse
	}
	serverClose = statusLine[7] == '0'

	contentLength := int64(-1)
	chunked := false
	for {
		line, err := readLine(reader)
		if err != nil {
			return 0, 0, false, err
		}
		if len(line) == 0 {
			break
		}
		name, value, ok := bytes.Cut(line, []byte(":"))
		if !ok {
			return 0, 0, false, errMalformedResponse
		}
		value = bytes.TrimSpace(value)
		switch {
		case bytes.EqualFold(name, []byte("Content-Length")):
			contentLength, err = strconv.ParseInt(string(value), 10, 64)
			if err != nil {
				return 0, 0, false, errMalformedResponse
			}
		case bytes.EqualFold(name, []byte("Transfer-Encoding")):
			chunked = bytes.Contains(bytes.ToLower(value), []byte("chunked"))
		case bytes.EqualFold(name, []byte("Connection")):
			if bytes.EqualFold(value, []byte("close")) {
				serverClose = true
			}
		}
	}

	switch {
	case status == 204 || status == 304 || (status >= 100 && status < 200):
	case chunked:
		bodyBytes, err = discardChunked(reader)
	case contentLength >= 0:
		var discarded int
		discarded, err = reader.Discard(int(contentLength))
		bodyBytes = int64(discarded)
	default:
		bodyBytes, err = io.Copy(io.Discard, reader)
		serverClose = true
	}
	return status, bodyBytes, serverClose, err
}

func discardChunked(reader *bufio.Reader) (int64, error) {
	var total int64
	for {
		line, err := readLine(reader)
		if err != nil {
			return total, err
		}
		sizeField, _, _ := bytes.Cut(line, []byte(";"))
		size, err := strconv.ParseInt(string(bytes.TrimSpace(sizeField)), 16, 64)
		if err != nil || size < 0 {
			return total, errMalformedResponse
		}
		if size == 0 {
			for {
				trailer, err := readLine(reader)
				if err != nil {
					return total, err
				}
				if len(trailer) == 0 {
					return total, nil
				}
			}
		}
		if _, err := reader.Discard(int(size) + 2); err != nil {
			return total, err
		}
		total += size
	}
}

func readLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && len(line) > 0 {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return bytes.TrimRight(line, "\r\n"), nil
}

func summarize(workers []*worker, wall, cpuUsed time.Duration, rate float64, dispatchLags []int64, clientCores int) Result {
	var all []int64
	var bodyBytes int64
	result := Result{TargetRPS: rate, WallSec: wall.Seconds()}
	for _, w := range workers {
		all = append(all, w.latencies...)
		bodyBytes += w.bodyBytes
		result.Errors += w.errors
		result.Non2xx += w.non2xx
	}
	slices.Sort(all)

	result.Requests = int64(len(all))
	result.RPS = float64(len(all)) / wall.Seconds()
	if len(all) > 0 {
		var sum float64
		for _, v := range all {
			sum += float64(v)
		}
		result.MeanUs = sum / float64(len(all)) / 1e3
		result.BodyBytesAvg = float64(bodyBytes) / float64(len(all))
		result.P50Us = percentileUs(all, 0.50)
		result.P90Us = percentileUs(all, 0.90)
		result.P99Us = percentileUs(all, 0.99)
		result.P999Us = percentileUs(all, 0.999)
		result.MaxUs = float64(all[len(all)-1]) / 1e3
	}

	if len(dispatchLags) > 0 {
		slices.Sort(dispatchLags)
		result.DispatchP50Us = percentileUs(dispatchLags, 0.50)
		result.DispatchP99Us = percentileUs(dispatchLags, 0.99)
	}

	result.ClientCores = clientCores
	result.ClientCPUSec = cpuUsed.Seconds()
	result.ClientUtil = cpuUsed.Seconds() / (wall.Seconds() * float64(result.ClientCores))
	return result
}

func percentileUs(sorted []int64, p float64) float64 {
	idx := int(p * float64(len(sorted)))
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return float64(sorted[idx]) / 1e3
}
