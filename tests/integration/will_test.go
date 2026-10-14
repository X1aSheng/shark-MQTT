// Package integration provides end-to-end integration tests for shark-mqtt.
// Run with: go test -race -tags=integration ./tests/integration/...
package integration

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// connectTestClient sends a CONNECT packet and verifies CONNACK.
func connectTestClient(t *testing.T, conn net.Conn, codec *protocol.Codec, clientID string) {
	t.Helper()
	connectPkt := &protocol.ConnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnect,
		},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
		},
		KeepAlive: 30,
		ClientID:  clientID,
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, connectPkt); err != nil {
		t.Fatalf("failed to send CONNECT: %v", err)
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("failed to read CONNACK: %v", err)
	}

	connAck, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}

	if connAck.ReasonCode != protocol.ReasonCodeSuccess {
		t.Fatalf("expected CONNACK accepted, got %d", connAck.ReasonCode)
	}
}

// subscribeToTopic sends SUBSCRIBE and verifies SUBACK.
func subscribeToTopic(t *testing.T, conn net.Conn, codec *protocol.Codec, topic string, qos uint8, packetID uint16) {
	t.Helper()
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeSubscribe,
			QoS:        1,
		},
		PacketID: packetID,
		Topics: []protocol.TopicFilter{
			{
				Topic: topic,
				QoS:   qos,
			},
		},
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, subPkt); err != nil {
		t.Fatalf("failed to send SUBSCRIBE: %v", err)
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("failed to read SUBACK: %v", err)
	}

	subAck, ok := pkt.(*protocol.SubAckPacket)
	if !ok {
		t.Fatalf("expected SUBACK, got %T", pkt)
	}

	if subAck.PacketID != packetID {
		t.Errorf("expected SUBACK packetID %d, got %d", packetID, subAck.PacketID)
	}
}

// disconnectClient sends DISCONNECT.
func disconnectClient(t *testing.T, conn net.Conn, codec *protocol.Codec) {
	t.Helper()
	disconnectPkt := &protocol.DisconnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeDisconnect,
		},
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, disconnectPkt); err != nil {
		t.Fatalf("failed to send DISCONNECT: %v", err)
	}
}

// TestWillMessageOnAbnormalDisconnect tests that a will message is published
// when a client disconnects without sending DISCONNECT.
func TestWillMessageOnAbnormalDisconnect(t *testing.T) {
	broker := testBroker(t)

	// Connect subscriber to will topic
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectTestClient(t, subConn, subCodec, "will-subscriber")
	subscribeToTopic(t, subConn, subCodec, "client1/will", 1, 1)

	// Connect client with will message
	willConn := dialTestBroker(t, broker)
	willCodec := protocol.NewCodec(0)

	connectPkt := &protocol.ConnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnect,
		},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
			WillFlag:     true,
			WillQoS:      1,
			WillRetain:   false,
		},
		KeepAlive:   30,
		ClientID:    "client1",
		WillTopic:   "client1/will",
		WillMessage: []byte("client1 offline"),
	}

	willConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := willCodec.Encode(willConn, connectPkt); err != nil {
		t.Fatalf("failed to send CONNECT with will: %v", err)
	}

	willConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := willCodec.Decode(willConn)
	if err != nil {
		t.Fatalf("failed to read CONNACK: %v", err)
	}

	connAck, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}

	if connAck.ReasonCode != protocol.ReasonCodeSuccess {
		t.Fatalf("expected CONNACK accepted, got %d", connAck.ReasonCode)
	}

	// Simulate abnormal disconnect (no DISCONNECT)
	willConn.Close()

	// Wait for will to be published
	time.Sleep(200 * time.Millisecond)

	// Check subscriber received the will message
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err = subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber did not receive will message: %v", err)
	}

	pubPkt, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}

	if pubPkt.Topic != "client1/will" {
		t.Errorf("expected topic client1/will, got %s", pubPkt.Topic)
	}

	if string(pubPkt.Payload) != "client1 offline" {
		t.Errorf("expected payload 'client1 offline', got %s", pubPkt.Payload)
	}

	subConn.Close()
}

// TestWillMessage_DelayedFires verifies a will with a delay interval is
// published AFTER the delay on abnormal disconnect. Previously disconnect()
// cancelled the just-armed delayed-will timer, so a delayed will never fired
// (P2-5b).
//
// The client must keep its Session alive across the disconnect for the delay to
// be observable at all: §3.1.3.2.2 publishes the Will Message when the Will
// Delay Interval has passed *or the Session ends, whichever happens first*, and
// §3.1.2.11.2 ends the Session with the Network Connection when the Session
// Expiry Interval is 0. Clean Start forces that interval to 0 in this broker,
// so the CONNECT below declares a Session Expiry Interval instead and leaves
// Clean Start unset. The counterpart -- a Clean Start client whose will is
// therefore published at once -- is TestWillMessage_CleanStartPublishesImmediately.
func TestWillMessage_DelayedFires(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectTestClient(t, subConn, subCodec, "will-delay-sub")
	subscribeToTopic(t, subConn, subCodec, "client/delayed-will", 1, 1)

	delay := uint32(1)          // seconds
	sessionExpiry := uint32(30) // seconds: must outlive the delay
	willConn := dialTestBroker(t, broker)
	willCodec := protocol.NewCodec(0)
	connectPkt := &protocol.ConnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnect,
		},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: false,
			WillFlag:     true,
			WillQoS:      1,
			WillRetain:   false,
		},
		KeepAlive:   30,
		ClientID:    "delayed-will-client",
		WillTopic:   "client/delayed-will",
		WillMessage: []byte("delayed-will-payload"),
		WillProperties: &protocol.Properties{
			WillDelayInterval: &delay,
		},
		Properties: &protocol.Properties{
			SessionExpiryInterval: &sessionExpiry,
		},
	}
	willConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := willCodec.Encode(willConn, connectPkt); err != nil {
		t.Fatalf("failed to send CONNECT with delayed will: %v", err)
	}
	if _, err := willCodec.Decode(willConn); err != nil {
		t.Fatalf("failed to read CONNACK: %v", err)
	}

	// Abnormal disconnect (no DISCONNECT).
	start := time.Now()
	willConn.Close()

	// The delayed will must arrive AFTER the delay interval.
	subConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("delayed will not delivered: %v", err)
	}
	pubPkt, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if pubPkt.Topic != "client/delayed-will" {
		t.Errorf("expected topic client/delayed-will, got %s", pubPkt.Topic)
	}
	if string(pubPkt.Payload) != "delayed-will-payload" {
		t.Errorf("expected payload 'delayed-will-payload', got %s", pubPkt.Payload)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Errorf("will fired after %v, expected after the delay interval (>= 900ms)", elapsed)
	}
	subConn.Close()
}

// TestWillMessageNotPublishedOnGracefulDisconnect tests that will is NOT published
// when client disconnects gracefully with DISCONNECT.
func TestWillMessageNotPublishedOnGracefulDisconnect(t *testing.T) {
	broker := testBroker(t)

	// Connect subscriber to will topic
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectTestClient(t, subConn, subCodec, "will-subscriber")
	subscribeToTopic(t, subConn, subCodec, "client2/will", 1, 1)

	// Connect client with will message
	willConn := dialTestBroker(t, broker)
	willCodec := protocol.NewCodec(0)

	connectPkt := &protocol.ConnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnect,
		},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
			WillFlag:     true,
			WillQoS:      1,
			WillRetain:   false,
		},
		KeepAlive:   30,
		ClientID:    "client2",
		WillTopic:   "client2/will",
		WillMessage: []byte("client2 offline"),
	}

	willConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := willCodec.Encode(willConn, connectPkt); err != nil {
		t.Fatalf("failed to send CONNECT with will: %v", err)
	}

	willConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := willCodec.Decode(willConn)
	if err != nil {
		t.Fatalf("failed to read CONNACK: %v", err)
	}

	connAck, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}

	if connAck.ReasonCode != protocol.ReasonCodeSuccess {
		t.Fatalf("expected CONNACK accepted, got %d", connAck.ReasonCode)
	}

	// Graceful disconnect
	disconnectClient(t, willConn, willCodec)
	willConn.Close()

	// Wait to ensure no will message is published
	time.Sleep(200 * time.Millisecond)

	// Check subscriber did NOT receive the will message
	subConn.SetDeadline(time.Now().Add(200 * time.Millisecond))
	_, err = subCodec.Decode(subConn)
	if err == nil {
		t.Error("expected no will message on graceful disconnect, but got one")
	}

	subConn.Close()
}

// TestWillMessageQoS tests will messages are published with the correct QoS.
func TestWillMessageQoS(t *testing.T) {
	tests := []struct {
		name     string
		willQoS  uint8
		expected uint8
	}{
		{"QoS 0", 0, 0},
		{"QoS 1", 1, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			broker := testBroker(t)

			subConn := dialTestBroker(t, broker)
			subCodec := protocol.NewCodec(0)
			connectClient(t, subConn, subCodec, "will-subscriber")
			subscribeToTopic(t, subConn, subCodec, "client/will", tc.willQoS, 1)

			willConn := dialTestBroker(t, broker)
			willCodec := protocol.NewCodec(0)

			connectPkt := &protocol.ConnectPacket{
				FixedHeader: protocol.FixedHeader{
					PacketType: protocol.PacketTypeConnect,
				},
				ProtocolName:    protocol.ProtocolNameMQTT,
				ProtocolVersion: protocol.Version50,
				Flags: protocol.ConnectFlags{
					CleanSession: true,
					WillFlag:     true,
					WillQoS:      tc.willQoS,
					WillRetain:   false,
				},
				KeepAlive:   30,
				ClientID:    "client",
				WillTopic:   "client/will",
				WillMessage: []byte("will message"),
			}

			willConn.SetDeadline(time.Now().Add(2 * time.Second))
			if err := willCodec.Encode(willConn, connectPkt); err != nil {
				t.Fatalf("failed to send CONNECT with will: %v", err)
			}

			willConn.SetDeadline(time.Now().Add(2 * time.Second))
			pkt, err := willCodec.Decode(willConn)
			if err != nil {
				t.Fatalf("failed to read CONNACK: %v", err)
			}

			if connAck, ok := pkt.(*protocol.ConnAckPacket); !ok || connAck.ReasonCode != protocol.ReasonCodeSuccess {
				t.Fatalf("expected CONNACK accepted")
			}

			// Abnormal disconnect
			willConn.Close()

			// Wait for will to be published
			time.Sleep(200 * time.Millisecond)

			subConn.SetDeadline(time.Now().Add(2 * time.Second))
			pkt, err = subCodec.Decode(subConn)
			if err != nil {
				t.Fatalf("subscriber did not receive will message: %v", err)
			}

			pubPkt, ok := pkt.(*protocol.PublishPacket)
			if !ok {
				t.Fatalf("expected PUBLISH, got %T", pkt)
			}

			if pubPkt.FixedHeader.QoS != tc.expected {
				t.Errorf("expected QoS %d, got %d", tc.expected, pubPkt.FixedHeader.QoS)
			}

			subConn.Close()
		})
	}
}

// connectV5WithWill connects an MQTT 5.0 client carrying a Will Message with
// the given properties, and returns the decoded CONNACK.
func connectV5WithWill(t *testing.T, conn net.Conn, codec *protocol.Codec, pkt *protocol.ConnectPacket) *protocol.ConnAckPacket {
	t.Helper()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		t.Fatalf("CONNECT with will: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	resp, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	ca, ok := resp.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", resp)
	}
	if ca.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("expected CONNACK accepted, got %d", ca.ReasonCode)
	}
	return ca
}

// awaitWillPublish reads one PUBLISH from a subscriber and fails the test if
// anything else arrives first.
func awaitWillPublish(t *testing.T, conn net.Conn, codec *protocol.Codec, within time.Duration) *protocol.PublishPacket {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(within))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("subscriber did not receive the will: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	return pub
}

// TestWillMessage_CleanStartPublishesImmediately covers the "or the Session
// ends, whichever happens first" half of MQTT 5.0 §3.1.3.2.2.
//
// A Session whose Expiry Interval is 0 ends when the Network Connection closes
// (§3.1.2.11.2), and this broker forces that interval for Clean Start
// connections. The Session therefore ends at the abnormal disconnect, well
// before the declared Will Delay Interval, so the Will Message must be
// published at once instead of five seconds later. Waiting out the delay here
// meant a subscriber heard about a dead client up to a full interval late, and
// a client that had already ended its Session could still leave a publication
// pending behind it.
func TestWillMessage_CleanStartPublishesImmediately(t *testing.T) {
	brk := testBroker(t)

	subConn := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectClient(t, subConn, subCodec, "will-cleanstart-sub")
	subscribeToTopic(t, subConn, subCodec, "client/cleanstart-will", 1, 1)

	willDelay := uint32(5)
	willConn := dialTestBroker(t, brk)
	willCodec := protocol.NewCodec(0)
	connectV5WithWill(t, willConn, willCodec, &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
			WillFlag:     true,
			WillQoS:      1,
		},
		KeepAlive:   30,
		ClientID:    "will-cleanstart-client",
		WillTopic:   "client/cleanstart-will",
		WillMessage: []byte("cleanstart-gone"),
		WillProperties: &protocol.Properties{
			WillDelayInterval: &willDelay,
		},
	})

	// The Session ends here, so the delay cannot apply.
	start := time.Now()
	willConn.Close()

	pub := awaitWillPublish(t, subConn, subCodec, 2*time.Second)
	if string(pub.Payload) != "cleanstart-gone" {
		t.Errorf("payload = %q, want %q", string(pub.Payload), "cleanstart-gone")
	}
	if elapsed := time.Since(start); elapsed >= 4*time.Second {
		t.Errorf("will published after %v: the Session ended inside the 5s Will Delay Interval, "+
			"so §3.1.3.2.2 requires publication at that point", elapsed)
	}
	subConn.Close()
}

// TestWillMessageSessionExpiryBoundsDelay covers the other ordering of the same
// rule: the Session outlives the network connection but not the Will Delay
// Interval, so the will is published when the Session ends rather than when the
// delay expires.
func TestWillMessageSessionExpiryBoundsDelay(t *testing.T) {
	brk := testBroker(t)

	subConn := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectClient(t, subConn, subCodec, "will-expiry-sub")
	subscribeToTopic(t, subConn, subCodec, "client/expiry-bounded-will", 1, 1)

	willDelay := uint32(30)
	sessionExpiry := uint32(1)
	willConn := dialTestBroker(t, brk)
	willCodec := protocol.NewCodec(0)
	connectV5WithWill(t, willConn, willCodec, &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: false,
			WillFlag:     true,
			WillQoS:      1,
		},
		KeepAlive:   30,
		ClientID:    "will-expiry-client",
		WillTopic:   "client/expiry-bounded-will",
		WillMessage: []byte("session-ended"),
		WillProperties: &protocol.Properties{
			WillDelayInterval: &willDelay,
		},
		Properties: &protocol.Properties{
			SessionExpiryInterval: &sessionExpiry,
		},
	})

	start := time.Now()
	willConn.Close()

	pub := awaitWillPublish(t, subConn, subCodec, 5*time.Second)
	elapsed := time.Since(start)
	if string(pub.Payload) != "session-ended" {
		t.Errorf("payload = %q, want %q", string(pub.Payload), "session-ended")
	}
	if elapsed < 800*time.Millisecond {
		t.Errorf("will published after %v, before the 1s Session Expiry Interval elapsed", elapsed)
	}
	if elapsed >= 10*time.Second {
		t.Errorf("will published after %v: the 1s Session ended long before the 30s Will Delay "+
			"Interval, so §3.1.3.2.2 requires publication at the Session end", elapsed)
	}
	subConn.Close()
}

// TestWillPropertiesRelayedToSubscriber covers MQTT 5.0 §3.1.3.2 + §3.3.2.3.
//
// The Will Message has a property section of its own, and once published it is
// an ordinary PUBLISH: the Server must relay the publisher's per-message
// properties to subscribers unaltered. Dropping them broke request/response
// for a will -- a responder could not be reached because its Response Topic
// never arrived -- and stripped the payload format and application tags.
//
// The Will Delay Interval is the one member of that section that must NOT be
// relayed: it instructs the Server about when to publish (§3.1.3.2.2) and is
// not a PUBLISH property at all, so forwarding it would put a property on the
// wire that §3.3.2.3 does not allow there.
func TestWillPropertiesRelayedToSubscriber(t *testing.T) {
	brk := testBroker(t)

	subConn := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectClient(t, subConn, subCodec, "will-props-sub")
	subscribeToTopic(t, subConn, subCodec, "client/will-with-props", 1, 1)

	payloadFormat := byte(1)
	expiry := uint32(30)
	willDelay := uint32(1)
	willConn := dialTestBroker(t, brk)
	willCodec := protocol.NewCodec(0)
	connectV5WithWill(t, willConn, willCodec, &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
			WillFlag:     true,
			WillQoS:      1,
		},
		KeepAlive:   30,
		ClientID:    "will-props-client",
		WillTopic:   "client/will-with-props",
		WillMessage: []byte(`{"state":"offline"}`),
		WillProperties: &protocol.Properties{
			PayloadFormat:         &payloadFormat,
			MessageExpiryInterval: &expiry,
			ContentType:           "application/json",
			ResponseTopic:         "client/will-with-props/reply",
			CorrelationData:       []byte("corr-7"),
			UserProperties:        []protocol.UserProperty{{Key: "source", Value: "will"}},
			WillDelayInterval:     &willDelay,
		},
	})

	willConn.Close()

	pub := awaitWillPublish(t, subConn, subCodec, 2*time.Second)
	if string(pub.Payload) != `{"state":"offline"}` {
		t.Errorf("payload = %q", string(pub.Payload))
	}
	got := pub.Properties
	if got == nil {
		t.Fatal("delivered will carries no property section: the Will Properties were dropped")
	}
	if got.PayloadFormat == nil || *got.PayloadFormat != payloadFormat {
		t.Errorf("PayloadFormat = %v, want %d", got.PayloadFormat, payloadFormat)
	}
	if got.ContentType != "application/json" {
		t.Errorf("ContentType = %q, want application/json", got.ContentType)
	}
	if got.ResponseTopic != "client/will-with-props/reply" {
		t.Errorf("ResponseTopic = %q", got.ResponseTopic)
	}
	if string(got.CorrelationData) != "corr-7" {
		t.Errorf("CorrelationData = %q, want corr-7", string(got.CorrelationData))
	}
	if len(got.UserProperties) != 1 || got.UserProperties[0].Key != "source" ||
		got.UserProperties[0].Value != "will" {
		t.Errorf("UserProperties = %+v, want the publisher's single pair", got.UserProperties)
	}
	if got.MessageExpiryInterval == nil {
		t.Error("MessageExpiryInterval missing: the will's own interval must be relayed")
	}
	if got.WillDelayInterval != nil {
		t.Errorf("WillDelayInterval = %d in the delivered PUBLISH: it is a CONNECT-scope "+
			"instruction, not a PUBLISH property", *got.WillDelayInterval)
	}
	subConn.Close()
}

// TestWillMessageFiresWhenCleanStartEndsPreviousSession covers the Clean Start
// half of §3.1.3.2.2 at the broker level.
//
// A client with a delay interval drops off; its Session outlives the network
// connection, so the will sits in the delay. The client then comes back with
// Clean Start set to 1, which ends that Session (§3.1.4) -- and a Will Message
// cannot outlive the Session it belongs to, so it has to be published at that
// moment rather than being left to a timer that now refers to nothing. This is
// the case EMQX shipped a fix for; the will was silently dropped entirely there,
// and here it would have been published up to a full interval late.
func TestWillMessageFiresWhenCleanStartEndsPreviousSession(t *testing.T) {
	brk := testBroker(t)

	subConn := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectClient(t, subConn, subCodec, "will-cs-end-sub")
	subscribeToTopic(t, subConn, subCodec, "client/cs-takeover-will", 1, 1)

	willDelay := uint32(30)
	sessionExpiry := uint32(60)
	firstConn := dialTestBroker(t, brk)
	firstCodec := protocol.NewCodec(0)
	connectV5WithWill(t, firstConn, firstCodec, &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: false,
			WillFlag:     true,
			WillQoS:      1,
		},
		KeepAlive:   30,
		ClientID:    "will-cs-end-client",
		WillTopic:   "client/cs-takeover-will",
		WillMessage: []byte("session-discarded"),
		WillProperties: &protocol.Properties{
			WillDelayInterval: &willDelay,
		},
		Properties: &protocol.Properties{
			SessionExpiryInterval: &sessionExpiry,
		},
	})

	start := time.Now()
	firstConn.Close()

	// Let the abnormal disconnect reach the broker and arm the delayed will.
	time.Sleep(200 * time.Millisecond)

	subConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, err := subCodec.Decode(subConn); err == nil {
		t.Fatal("will published before the 30s delay elapsed")
	}

	// Clean Start ends the previous Session, which publishes the pending will.
	secondConn := dialTestBroker(t, brk)
	secondCodec := protocol.NewCodec(0)
	connectV5WithWill(t, secondConn, secondCodec, &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "will-cs-end-client",
	})

	pub := awaitWillPublish(t, subConn, subCodec, 2*time.Second)
	if string(pub.Payload) != "session-discarded" {
		t.Errorf("payload = %q, want %q", string(pub.Payload), "session-discarded")
	}
	if elapsed := time.Since(start); elapsed >= 10*time.Second {
		t.Errorf("will published after %v: the Clean Start reconnect ended the Session long before "+
			"the 30s delay, so §3.1.3.2.2 requires publication at that point", elapsed)
	}
	subConn.Close()
	secondConn.Close()
}
