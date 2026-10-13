package broker

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestForwardedPublishProperties covers the MQTT 5.0 §3.3.2.3 relay rules: the
// publisher's per-message properties reach the subscriber unaltered (User
// Property order included), the Subscription Identifier and the decremented
// Message Expiry Interval are the Server's own additions, and the Topic Alias
// -- which names a mapping in the publisher's inbound alias space -- is never
// relayed.
func TestForwardedPublishProperties(t *testing.T) {
	pfi := byte(1)
	alias := uint16(7)
	userProps := []protocol.UserProperty{
		{Key: "trace", Value: "1"},
		{Key: "tenant", Value: "acme"},
		{Key: "trace", Value: "2"}, // same key twice: order and duplicates MUST survive
	}
	publisher := &protocol.Properties{
		PayloadFormat:   &pfi,
		ContentType:     "application/json",
		ResponseTopic:   "reply/to/me",
		CorrelationData: []byte{0xDE, 0xAD, 0xBE, 0xEF},
		UserProperties:  userProps,
		TopicAlias:      &alias,
	}

	t.Run("publisher properties relayed unaltered", func(t *testing.T) {
		got := forwardedPublishProperties(publisher, nil, time.Time{})
		if got == nil {
			t.Fatal("expected the publisher's properties to be forwarded")
		}
		if got.PayloadFormat == nil || *got.PayloadFormat != pfi {
			t.Errorf("PayloadFormat = %v, want %d", got.PayloadFormat, pfi)
		}
		if got.ContentType != "application/json" {
			t.Errorf("ContentType = %q, want %q", got.ContentType, "application/json")
		}
		if got.ResponseTopic != "reply/to/me" {
			t.Errorf("ResponseTopic = %q, want %q", got.ResponseTopic, "reply/to/me")
		}
		if string(got.CorrelationData) != string([]byte{0xDE, 0xAD, 0xBE, 0xEF}) {
			t.Errorf("CorrelationData = %x, want deadbeef", got.CorrelationData)
		}
		if len(got.UserProperties) != len(userProps) {
			t.Fatalf("UserProperties count = %d, want %d", len(got.UserProperties), len(userProps))
		}
		for i, want := range userProps {
			if got.UserProperties[i] != want {
				t.Errorf("UserProperties[%d] = %+v, want %+v (order must be preserved)",
					i, got.UserProperties[i], want)
			}
		}
		if got.TopicAlias != nil {
			t.Error("Topic Alias names the publisher's inbound alias mapping and must not be relayed")
		}
		if got.SubscriptionIdentifier != nil {
			t.Error("SubscriptionIdentifier must only be set from the matching subscription")
		}
		if got.MessageExpiryInterval != nil {
			t.Error("MessageExpiryInterval must only be set when the publisher sent one")
		}
	})

	t.Run("nothing to relay and nothing to add stays nil", func(t *testing.T) {
		if got := forwardedPublishProperties(nil, nil, time.Time{}); got != nil {
			t.Errorf("nil publisher properties: got %+v, want nil so the encoded packet is unchanged", got)
		}
		if got := forwardedPublishProperties(&protocol.Properties{}, nil, time.Time{}); got != nil {
			t.Errorf("empty publisher properties: got %+v, want nil", got)
		}
	})

	t.Run("server additions combine with relayed properties", func(t *testing.T) {
		subID := uint32(42)
		expiresAt := time.Now().Add(30 * time.Second)
		got := forwardedPublishProperties(publisher, &subID, expiresAt)
		if got == nil {
			t.Fatal("expected a property set")
		}
		if got.SubscriptionIdentifier == nil || *got.SubscriptionIdentifier != subID {
			t.Errorf("SubscriptionIdentifier = %v, want %d", got.SubscriptionIdentifier, subID)
		}
		if got.MessageExpiryInterval == nil {
			t.Fatal("expected the remaining Message Expiry Interval to be advertised")
		}
		if v := *got.MessageExpiryInterval; v < 25 || v > 30 {
			t.Errorf("MessageExpiryInterval = %d, want the remaining ~30s", v)
		}
		if got.ResponseTopic != "reply/to/me" || got.ContentType != "application/json" {
			t.Error("adding the server's own properties must not drop the relayed ones")
		}
	})

	t.Run("an elapsed deadline advertises 1s, never 0", func(t *testing.T) {
		// 0 means "already expired" on the wire, so a message that only survives
		// because the clock has not passed the deadline yet must not announce 0.
		got := forwardedPublishProperties(nil, nil, time.Now().Add(-time.Hour))
		if got == nil || got.MessageExpiryInterval == nil {
			t.Fatal("expected a Message Expiry Interval")
		}
		if *got.MessageExpiryInterval != 1 {
			t.Errorf("MessageExpiryInterval = %d, want 1", *got.MessageExpiryInterval)
		}
	})
}

// TestRepublishKeepsForwardedProperties guards the retry path: a QoS 1/2
// retransmission is the same Application Message, so it must still carry the
// properties the relay rules require. Before this, republish rebuilt a bare
// PUBLISH and a subscriber whose first copy was lost would never see the
// Response Topic or the publisher's User Properties.
func TestRepublishKeepsForwardedProperties(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	const clientID = "retry-props"
	sess := b.sessions.CreateSession(clientID, &protocol.ConnectPacket{
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        clientID,
	}, false)
	if !sess.Spec().Properties {
		t.Fatalf("test setup: session negotiated version %d, want MQTT 5.0", sess.ProtocolVer)
	}

	subID := uint32(11)
	sess.AddInflight(&InflightMsg{
		PacketID:  7,
		QoS:       1,
		Topic:     "props/retry",
		Payload:   []byte("payload"),
		ExpiresAt: time.Now().Add(30 * time.Second),
		Properties: &protocol.Properties{
			ContentType:            "application/json",
			ResponseTopic:          "reply/here",
			CorrelationData:        []byte{0x01, 0x02},
			UserProperties:         []protocol.UserProperty{{Key: "trace", Value: "abc"}},
			SubscriptionIdentifier: &subID,
		},
	})

	// A buffered outbound queue lets the test read the packet object itself
	// instead of decoding wire bytes, so the assertion is on what the broker
	// decided to send.
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	out := make(chan protocol.Packet, 4)
	b.mu.Lock()
	b.connections[clientID] = &clientState{
		conn:       serverConn,
		codec:      protocol.NewCodec(0),
		out:        out,
		stopWrites: make(chan struct{}),
	}
	b.mu.Unlock()

	if err := b.republish(clientID, 7, "props/retry", []byte("payload"), 1, false); err != nil {
		t.Fatalf("republish: %v", err)
	}

	select {
	case pkt := <-out:
		pub, ok := pkt.(*protocol.PublishPacket)
		if !ok {
			t.Fatalf("expected a PUBLISH, got %T", pkt)
		}
		if !pub.Dup {
			t.Error("a retransmission must set DUP")
		}
		props := pub.Properties
		if props == nil {
			t.Fatal("retransmission dropped the forwarded properties")
		}
		if props.ContentType != "application/json" {
			t.Errorf("ContentType = %q, want application/json", props.ContentType)
		}
		if props.ResponseTopic != "reply/here" {
			t.Errorf("ResponseTopic = %q, want reply/here", props.ResponseTopic)
		}
		if string(props.CorrelationData) != string([]byte{0x01, 0x02}) {
			t.Errorf("CorrelationData = %x, want 0102", props.CorrelationData)
		}
		if len(props.UserProperties) != 1 || props.UserProperties[0].Key != "trace" {
			t.Errorf("UserProperties = %+v, want the publisher's single trace pair", props.UserProperties)
		}
		if props.SubscriptionIdentifier == nil || *props.SubscriptionIdentifier != subID {
			t.Errorf("SubscriptionIdentifier = %v, want %d", props.SubscriptionIdentifier, subID)
		}
		if props.MessageExpiryInterval == nil || *props.MessageExpiryInterval < 25 {
			t.Errorf("MessageExpiryInterval = %v, want the recomputed remaining ~30s",
				props.MessageExpiryInterval)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("republish wrote no packet")
	}
}
