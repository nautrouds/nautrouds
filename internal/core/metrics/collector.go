package metrics

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/nautrouds/tentacle-metrics/pb"

	"github.com/sigurn/crc16"
	"google.golang.org/protobuf/proto"
)

var crcTable = crc16.MakeTable(crc16.CRC16_X_25)

const (
	MagicByte    = 0xBE
	ProtoVersion = 0x01
	MaxFrameSize = 1024 * 1024 // 1MB limit for safety
)

// Collector handles the UDS server for receiving metrics
type Collector struct {
	socketPath string
	socketMode os.FileMode
	listener   net.Listener
	registry   *Registry
}

func NewCollector(socketPath string, socketMode os.FileMode, r *Registry) *Collector {
	return &Collector{
		socketPath: socketPath,
		socketMode: socketMode,
		registry:   r,
	}
}

func (c *Collector) Start() error {
	if err := os.Remove(c.socketPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	l, err := net.Listen("unix", c.socketPath)
	if err != nil {
		return err
	}

	os.Chmod(c.socketPath, c.socketMode)

	c.listener = l
	go c.acceptLoop()
	return nil
}

func (c *Collector) Stop() {
	if c.listener != nil {
		c.listener.Close()
	}
}

func (c *Collector) acceptLoop() {
	for {
		conn, err := c.listener.Accept()
		if err != nil {
			return
		}
		go c.handleConnection(conn)
	}
}

func (c *Collector) handleConnection(conn net.Conn) {
	defer conn.Close()

	for {
		// 1. Read Header (Magic + Version + Length = 6 bytes)
		header := make([]byte, 6)
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}

		if header[0] != MagicByte || header[1] != ProtoVersion {
			fmt.Printf("Invalid metrics frame header: %x\n", header[:2])
			return
		}

		length := binary.BigEndian.Uint32(header[2:6])
		if length > MaxFrameSize {
			fmt.Printf("Metrics frame too large: %d\n", length)
			return
		}

		// 2. Read Payload
		payload := make([]byte, length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}

		// 3. Read Checksum (2 bytes)
		checksumBuf := make([]byte, 2)
		if _, err := io.ReadFull(conn, checksumBuf); err != nil {
			return
		}
		receivedChecksum := binary.BigEndian.Uint16(checksumBuf)

		fullData := append(header, payload...)
		calcChecksum := crc16.Checksum(fullData, crcTable)

		if calcChecksum != receivedChecksum {
			fmt.Printf("CRC16 Checksum mismatch: expected %04x, got %04x\n", calcChecksum, receivedChecksum)
			return
		}

		// 4. Decode and Update
		c.processPayload(payload)
	}
}

func (c *Collector) processPayload(data []byte) {
	var msg pb.MetricPayload
	if err := proto.Unmarshal(data, &msg); err != nil {
		fmt.Printf("Failed to unmarshal metrics payload: %v\n", err)
		return
	}

	for _, m := range msg.Metrics {
		switch m.Name {
		case "tentacle_active_connections":
			if m.Type == pb.Metric_GAUGE {
				c.registry.TentacleActiveConnections.WithLabelValues(
					msg.TentacleId, msg.Service,
				).Set(m.Value)
			}
		case "tentacle_connection_attempts_total":
			if m.Type == pb.Metric_COUNTER {
				c.registry.TentacleConnectionAttemptsTotal.WithLabelValues(
					msg.TentacleId, msg.Service,
				).Add(m.Value)
			}
		case "tentacle_connection_failures_total":
			if m.Type == pb.Metric_COUNTER {
				h := c.registry.TentacleConnectionFailuresTotal.WithLabelValues(
					msg.TentacleId, msg.Service,
				)
				h.Add(m.Value)
			}
		case "tentacle_bytes_transmitted_total":
			if m.Type == pb.Metric_COUNTER {
				c.registry.TentacleBytesTransmittedTotal.WithLabelValues(
					msg.TentacleId, msg.Service,
				).Add(m.Value)
			}
		case "tentacle_transport_latency_seconds":
			if m.Type == pb.Metric_HISTOGRAM {
				observer := c.registry.TentacleTransportLatency.WithLabelValues(
					msg.TentacleId, msg.Service,
				)
				if len(m.Buckets) > 0 {
					var prevCount uint64
					for _, bucket := range m.Buckets {
						if bucket.Count <= prevCount {
							continue
						}
						delta := bucket.Count - prevCount
						prevCount = bucket.Count
						for range delta {
							observer.Observe(bucket.Le)
						}
					}
				} else {
					observer.Observe(m.Value)
				}
			}
		}
	}
}
