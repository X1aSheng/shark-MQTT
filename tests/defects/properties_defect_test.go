package defects

import (
	"bytes"
	"strings"
	"testing"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestDefectLongMQTT5PropertyStringReturnsEncodeError reproduces the defect
// where a property string longer than 65535 bytes was encoded with a truncated
// length prefix, producing a frame the peer could not parse.
//
// The codec must be bound to MQTT 5.0 explicitly: properties are v5 wire syntax
// only, so a pre-v5 codec refuses a properties-bearing PUBLISH outright
// (protocol.ErrUnsupportedVersion) and the oversize bound would never be
// reached. Binding the version is what makes this test exercise the defect
// rather than the version gate.
func TestDefectLongMQTT5PropertyStringReturnsEncodeError(t *testing.T) {
	codec := protocol.NewCodecForVersion(512*1024, protocol.Version50)
	pkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
		Topic:       "defect/properties",
		Properties: &protocol.Properties{
			ContentType: strings.Repeat("x", 65536),
		},
		Payload: []byte("payload"),
	}

	var buf bytes.Buffer
	if err := codec.Encode(&buf, pkt); err == nil {
		t.Fatal("Encode() succeeded with an MQTT UTF-8 string longer than 65535 bytes")
	}
}
