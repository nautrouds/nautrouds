package forwarder

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"time"
)

const (
	// RFC 7540 §3.5 connection preface.
	http2ClientPreface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

	frameTypeSettings = 0x4
	frameTypeGoAway   = 0x7

	probeDialTimeout = 500 * time.Millisecond
	probeIOTimeout   = 250 * time.Millisecond
)

const (
	reasonSupported   = "supported"
	reasonDeclined    = "declined"
	reasonUnsupported = "unsupported"
	reasonTimeout     = "timeout"
	reasonDialFailed  = "dial_failed"
)

// length=0, type=SETTINGS, flags=0, stream ID=0.
var emptySettingsFrame = [9]byte{0x00, 0x00, 0x00, frameTypeSettings, 0x00, 0x00, 0x00, 0x00, 0x00}

// probeHTTP2 never opens a stream (no HEADERS frame), so there's no "request" to reject.
func probeHTTP2(nodePath string) (useHTTP2 bool, reason string) {
	conn, err := net.DialTimeout("unix", nodePath, probeDialTimeout)
	if err != nil {
		return false, reasonDialFailed
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(probeIOTimeout))

	payload := append([]byte(http2ClientPreface), emptySettingsFrame[:]...)
	if _, err := conn.Write(payload); err != nil {
		return classifyProbeErr(err)
	}

	var hdr [9]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return classifyProbeErr(err)
	}

	// SETTINGS/GOAWAY are connection-level frames, so stream ID must be 0.
	streamID := binary.BigEndian.Uint32(hdr[5:9]) &^ (1 << 31)
	if streamID != 0 {
		return false, reasonUnsupported
	}

	switch hdr[3] {
	case frameTypeSettings:
		return true, reasonSupported
	case frameTypeGoAway:
		return false, reasonDeclined
	default:
		return false, reasonUnsupported
	}
}

func classifyProbeErr(err error) (bool, string) {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return false, reasonTimeout
	}
	return false, reasonUnsupported
}
