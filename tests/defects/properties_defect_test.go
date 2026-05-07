package defects

import (
	"bytes"
	"strings"
	"testing"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

func TestDefectLongMQTT5PropertyStringReturnsEncodeError(t *testing.T) {
	codec := protocol.NewCodec(512 * 1024)
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
