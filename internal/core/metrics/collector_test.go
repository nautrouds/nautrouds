package metrics_test

import (
	"encoding/binary"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nautrouds/internal/core/metrics"

	"github.com/nautrouds/tentacle-metrics/pb"
	"github.com/sigurn/crc16"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

var crcTable = crc16.MakeTable(crc16.CRC16_X_25)

func buildFrame(t *testing.T, msg *pb.MetricPayload) []byte {
	t.Helper()
	payload, err := proto.Marshal(msg)
	require.NoError(t, err)

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

func dialCollector(t *testing.T) net.Conn {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "metrics.sock")
	c := metrics.NewCollector(sockPath, 0666, metrics.Global)
	require.NoError(t, c.Start())
	t.Cleanup(c.Stop)

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestCollector_StartRemovesStaleSocket(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "metrics.sock")
	require.NoError(t, os.WriteFile(sockPath, []byte("stale"), 0644))

	c := metrics.NewCollector(sockPath, 0666, metrics.Global)
	require.NoError(t, c.Start())
	c.Stop()
}

func TestCollector_ValidFrame(t *testing.T) {
	tests := []struct {
		name    string
		metrics []*pb.Metric
	}{
		{"Gauge", []*pb.Metric{
			{Name: "tentacle_active_connections", Type: pb.Metric_GAUGE, Value: 7},
		}},
		{"Counter", []*pb.Metric{
			{Name: "tentacle_connection_attempts_total", Type: pb.Metric_COUNTER, Value: 3},
			{Name: "tentacle_connection_failures_total", Type: pb.Metric_COUNTER, Value: 1},
			{Name: "tentacle_bytes_transmitted_total", Type: pb.Metric_COUNTER, Value: 1024},
		}},
		{"Histogram", []*pb.Metric{
			{
				Name: "tentacle_transport_latency_seconds",
				Type: pb.Metric_HISTOGRAM,
				Buckets: []*pb.Bucket{
					{Le: 0.005, Count: 2},
					{Le: 0.01, Count: 1},
				},
			},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := dialCollector(t)
			tentacleID := "test-" + tt.name
			idLabel := `tentacle_id="` + tentacleID + `"`

			frame := buildFrame(t, &pb.MetricPayload{
				TentacleId: tentacleID,
				Service:    "test-svc",
				Metrics:    tt.metrics,
			})
			_, err := conn.Write(frame)
			require.NoError(t, err)

			for _, m := range tt.metrics {
				assert.Eventually(t, func() bool {
					w := httptest.NewRecorder()
					req := httptest.NewRequest("GET", "/metrics", nil)
					metrics.Global.WritePrometheus(w, req)
					body := w.Body.String()
					for line := range strings.SplitSeq(body, "\n") {
						if strings.HasPrefix(line, m.Name) && strings.Contains(line, idLabel) {
							return true
						}
					}
					return false
				}, time.Second, 10*time.Millisecond, m.Name)
			}
		})
	}
}

func TestCollector_InvalidFrameClosesConnection(t *testing.T) {
	oversizedHeader := make([]byte, 6)
	oversizedHeader[0] = metrics.MagicByte
	oversizedHeader[1] = metrics.ProtoVersion
	binary.BigEndian.PutUint32(oversizedHeader[2:], metrics.MaxFrameSize+1)

	crcMismatch := buildFrame(t, &pb.MetricPayload{TentacleId: "x", Service: "y"})
	crcMismatch[len(crcMismatch)-1] ^= 0xFF

	tests := []struct {
		name  string
		frame []byte
	}{
		{"InvalidMagicByte", []byte{0xFF, metrics.ProtoVersion, 0, 0, 0, 0, 0, 0}},
		{"CRCMismatch", crcMismatch},
		{"FrameTooLarge", oversizedHeader},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := dialCollector(t)
			conn.Write(tt.frame)

			deadline := time.Now().Add(500 * time.Millisecond)
			conn.SetReadDeadline(deadline)
			_, err := conn.Read(make([]byte, 1))
			assert.Error(t, err)
		})
	}
}
