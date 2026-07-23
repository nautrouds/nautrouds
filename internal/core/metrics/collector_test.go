package metrics_test

import (
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nautrouds/internal/core/metrics"

	"github.com/nautrouds/tentacle-metrics/pb"
	"github.com/sigurn/crc16"
	"google.golang.org/protobuf/proto"
)

var crcTable = crc16.MakeTable(crc16.CRC16_X_25)

// buildFrame encodes a MetricPayload into a wire frame (header + payload + CRC16).
func buildFrame(t *testing.T, msg *pb.MetricPayload) []byte {
	t.Helper()
	payload, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}

	header := make([]byte, 6)
	header[0] = metrics.MagicByte
	header[1] = metrics.ProtoVersion
	binary.BigEndian.PutUint32(header[2:], uint32(len(payload)))

	full := append(header, payload...)
	checksum := crc16.Checksum(full, crcTable)

	frame := make([]byte, len(full)+2)
	copy(frame, full)
	binary.BigEndian.PutUint16(frame[len(full):], checksum)
	return frame
}

func TestCollector_StartStop(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "collector-basic-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	sockPath := filepath.Join(tmpDir, "metrics.sock")
	c := metrics.NewCollector(sockPath, 0666, metrics.Global)

	if err := c.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	c.Stop()
}

func TestCollector_StartRemovesStaleSocket(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "collector-stale-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	sockPath := filepath.Join(tmpDir, "metrics.sock")
	// Create a stale file at the socket path before starting
	os.WriteFile(sockPath, []byte("stale"), 0644)

	c := metrics.NewCollector(sockPath, 0666, metrics.Global)
	if err := c.Start(); err != nil {
		t.Fatalf("Start should remove stale file and succeed: %v", err)
	}
	c.Stop()
}

func TestCollector_ValidGaugeFrame(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "collector-gauge-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	sockPath := filepath.Join(tmpDir, "metrics.sock")
	c := metrics.NewCollector(sockPath, 0666, metrics.Global)
	if err := c.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop()

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	frame := buildFrame(t, &pb.MetricPayload{
		TentacleId: "test-t1",
		Service:    "test-svc",
		Metrics: []*pb.Metric{
			{Name: "tentacle_active_connections", Type: pb.Metric_GAUGE, Value: 7},
		},
	})
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Give the goroutine time to process the frame
	time.Sleep(50 * time.Millisecond)
}

func TestCollector_ValidCounterFrame(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "collector-counter-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	sockPath := filepath.Join(tmpDir, "metrics.sock")
	c := metrics.NewCollector(sockPath, 0666, metrics.Global)
	if err := c.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop()

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	frame := buildFrame(t, &pb.MetricPayload{
		TentacleId: "test-t2",
		Service:    "test-svc",
		Metrics: []*pb.Metric{
			{Name: "tentacle_connection_attempts_total", Type: pb.Metric_COUNTER, Value: 3},
			{Name: "tentacle_connection_failures_total", Type: pb.Metric_COUNTER, Value: 1},
			{Name: "tentacle_bytes_transmitted_total", Type: pb.Metric_COUNTER, Value: 1024},
		},
	})
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("Write: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
}

func TestCollector_ValidHistogramFrame(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "collector-hist-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	sockPath := filepath.Join(tmpDir, "metrics.sock")
	c := metrics.NewCollector(sockPath, 0666, metrics.Global)
	if err := c.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop()

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	frame := buildFrame(t, &pb.MetricPayload{
		TentacleId: "test-t3",
		Service:    "test-svc",
		Metrics: []*pb.Metric{
			{
				Name: "tentacle_transport_latency_seconds",
				Type: pb.Metric_HISTOGRAM,
				Buckets: []*pb.Bucket{
					{Le: 0.005, Count: 2},
					{Le: 0.01, Count: 1},
				},
			},
		},
	})
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("Write: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
}

func TestCollector_InvalidMagicByte(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "collector-magic-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	sockPath := filepath.Join(tmpDir, "metrics.sock")
	c := metrics.NewCollector(sockPath, 0666, metrics.Global)
	if err := c.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop()

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Send a frame with wrong magic byte — server should close the connection
	badHeader := []byte{0xFF, metrics.ProtoVersion, 0, 0, 0, 0, 0, 0}
	conn.Write(badHeader)

	// Verify server closed the connection
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1)
	_, readErr := conn.Read(buf)
	if readErr == nil {
		t.Error("expected connection to be closed after invalid magic byte")
	}
}

func TestCollector_CRCMismatch(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "collector-crc-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	sockPath := filepath.Join(tmpDir, "metrics.sock")
	c := metrics.NewCollector(sockPath, 0666, metrics.Global)
	if err := c.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop()

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Build a valid frame then corrupt the checksum
	frame := buildFrame(t, &pb.MetricPayload{TentacleId: "x", Service: "y"})
	frame[len(frame)-1] ^= 0xFF // flip last byte of checksum

	conn.Write(frame)

	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1)
	_, readErr := conn.Read(buf)
	if readErr == nil {
		t.Error("expected connection to be closed after CRC mismatch")
	}
}

func TestCollector_FrameTooLarge(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "collector-large-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	sockPath := filepath.Join(tmpDir, "metrics.sock")
	c := metrics.NewCollector(sockPath, 0666, metrics.Global)
	if err := c.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop()

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Send a header claiming length > MaxFrameSize
	header := make([]byte, 6)
	header[0] = metrics.MagicByte
	header[1] = metrics.ProtoVersion
	binary.BigEndian.PutUint32(header[2:], metrics.MaxFrameSize+1)
	conn.Write(header)

	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1)
	_, readErr := conn.Read(buf)
	if readErr == nil {
		t.Error("expected connection to be closed for oversized frame")
	}
}
