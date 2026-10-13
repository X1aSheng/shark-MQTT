package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The badger and redis session stores persist a whole SessionData with
// encoding/json (store/badger/session_store.go, store/redis/session_store.go),
// so the JSON layout of InflightMessage is a durable format -- not an internal
// detail. These tests exercise that format directly, without needing either
// backend or a build tag.

func TestInflightMessagePropertiesJSONRoundTrip(t *testing.T) {
	payloadFormat := byte(1)
	subscriptionID := uint32(7)
	deadline := time.Date(2026, 10, 13, 2, 30, 15, 123456789, time.UTC)

	data := &SessionData{
		ClientID: "json-client",
		Inflight: map[uint16]*InflightMessage{
			42: {
				PacketID: 42,
				QoS:      1,
				Topic:    "sensors/temp",
				Payload:  []byte("21.5"),
				Retain:   true,
				Properties: MessageProperties{
					PayloadFormat:   &payloadFormat,
					ContentType:     "application/json",
					ResponseTopic:   "replies/temp",
					CorrelationData: []byte{0x00, 0xFF, 0x10},
					UserProperties: []UserProperty{
						{Key: "trace", Value: "abc"},
						{Key: "trace", Value: "def"}, // order and repetition are significant
						{Key: "tenant", Value: "acme"},
					},
					SubscriptionIdentifier: &subscriptionID,
				},
				ExpiresAt: deadline,
			},
		},
		InboundQoS2: map[uint16]*InflightMessage{
			7: {
				PacketID: 7,
				QoS:      2,
				Topic:    "orders/new",
				Payload:  []byte("{}"),
				Properties: MessageProperties{
					ResponseTopic: "orders/ack",
				},
			},
		},
	}

	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}

	var got SessionData
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal session: %v", err)
	}

	out := got.Inflight[42]
	if out == nil {
		t.Fatal("inflight entry 42 did not survive the round trip")
	}
	if out.PacketID != 42 || out.QoS != 1 || out.Topic != "sensors/temp" || !out.Retain {
		t.Errorf("core fields drifted: %+v", out)
	}
	if string(out.Payload) != "21.5" {
		t.Errorf("Payload = %q, want %q", out.Payload, "21.5")
	}
	if !out.ExpiresAt.Equal(deadline) {
		// time.Time marshals as RFC3339 with a nanosecond fraction; the
		// deadline has to survive intact or §3.3.2.3.2 drifts after a restart.
		t.Errorf("ExpiresAt = %v, want %v", out.ExpiresAt, deadline)
	}
	if out.Properties.PayloadFormat == nil || *out.Properties.PayloadFormat != 1 {
		t.Errorf("PayloadFormat = %v, want pointer to 1", out.Properties.PayloadFormat)
	}
	if out.Properties.ContentType != "application/json" {
		t.Errorf("ContentType = %q", out.Properties.ContentType)
	}
	if out.Properties.ResponseTopic != "replies/temp" {
		t.Errorf("ResponseTopic = %q", out.Properties.ResponseTopic)
	}
	if string(out.Properties.CorrelationData) != string([]byte{0x00, 0xFF, 0x10}) {
		t.Errorf("CorrelationData = %v, want [0 255 16]", out.Properties.CorrelationData)
	}
	if len(out.Properties.UserProperties) != 3 {
		t.Fatalf("UserProperties = %v, want 3 pairs", out.Properties.UserProperties)
	}
	wantPairs := []UserProperty{
		{Key: "trace", Value: "abc"},
		{Key: "trace", Value: "def"},
		{Key: "tenant", Value: "acme"},
	}
	for i, want := range wantPairs {
		if out.Properties.UserProperties[i] != want {
			t.Errorf("UserProperties[%d] = %+v, want %+v (relative order is significant, §3.3.2.3.7)",
				i, out.Properties.UserProperties[i], want)
		}
	}
	if out.Properties.SubscriptionIdentifier == nil || *out.Properties.SubscriptionIdentifier != 7 {
		t.Errorf("SubscriptionIdentifier = %v, want pointer to 7", out.Properties.SubscriptionIdentifier)
	}

	in := got.InboundQoS2[7]
	if in == nil {
		t.Fatal("inbound QoS 2 entry 7 did not survive the round trip")
	}
	if in.Properties.ResponseTopic != "orders/ack" {
		t.Errorf("inbound ResponseTopic = %q, want %q", in.Properties.ResponseTopic, "orders/ack")
	}
	if in.ExpiresAt.IsZero() != true {
		t.Errorf("inbound ExpiresAt = %v, want the zero time", in.ExpiresAt)
	}
}

// TestInflightMessageLegacyJSONStillLoads pins the other half of the format
// claim: the two new fields are purely additive, so a snapshot written before
// they existed still decodes. The fixture is the exact shape the previous
// version produced.
func TestInflightMessageLegacyJSONStillLoads(t *testing.T) {
	const legacy = `{"PacketID":1,"QoS":1,"Topic":"sensors/temp","Payload":"aGVsbG8=","Retain":false}`

	var got InflightMessage
	if err := json.Unmarshal([]byte(legacy), &got); err != nil {
		t.Fatalf("a pre-existing snapshot no longer decodes: %v", err)
	}
	if got.PacketID != 1 || got.Topic != "sensors/temp" || string(got.Payload) != "hello" {
		t.Errorf("legacy fields drifted: %+v", got)
	}
	// MessageProperties holds slices, so it is not comparable; check the fields
	// the same way the broker's zero test (protocolProperties) has to.
	if got.Properties.PayloadFormat != nil || got.Properties.ContentType != "" ||
		got.Properties.ResponseTopic != "" || got.Properties.CorrelationData != nil ||
		got.Properties.UserProperties != nil || got.Properties.SubscriptionIdentifier != nil {
		t.Errorf("Properties = %+v, want the zero value", got.Properties)
	}
	if !got.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt = %v, want the zero time", got.ExpiresAt)
	}
}

// TestInflightMessageJSONPreservesNilSlices guards the nil-versus-empty
// distinction. The broker decides whether to rebuild a property section at all
// by testing these fields against nil, so JSON turning a nil slice into an
// empty one (or the reverse) would change what a restored session puts on the
// wire.
func TestInflightMessageJSONPreservesNilSlices(t *testing.T) {
	t.Run("nil stays nil", func(t *testing.T) {
		encoded, err := json.Marshal(&InflightMessage{PacketID: 1})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var got InflightMessage
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.Properties.CorrelationData != nil {
			t.Errorf("CorrelationData = %v, want nil", got.Properties.CorrelationData)
		}
		if got.Properties.UserProperties != nil {
			t.Errorf("UserProperties = %v, want nil", got.Properties.UserProperties)
		}
		if got.Properties.PayloadFormat != nil {
			t.Errorf("PayloadFormat = %v, want nil", got.Properties.PayloadFormat)
		}
		if got.Properties.SubscriptionIdentifier != nil {
			t.Errorf("SubscriptionIdentifier = %v, want nil", got.Properties.SubscriptionIdentifier)
		}
	})

	t.Run("empty stays empty", func(t *testing.T) {
		encoded, err := json.Marshal(&InflightMessage{
			PacketID: 1,
			Properties: MessageProperties{
				CorrelationData: []byte{},
				UserProperties:  []UserProperty{},
			},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var got InflightMessage
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.Properties.CorrelationData == nil {
			t.Error("an empty CorrelationData decoded back to nil")
		}
		if got.Properties.UserProperties == nil {
			t.Error("an empty UserProperties decoded back to nil")
		}
	})
}

// TestInflightMessageLegacySnapshotIgnoresUnknownPropertyKeys is the forward
// half of the compatibility story: a snapshot written by a newer build, with
// property keys this build does not know, must still load rather than fail the
// whole session restore.
func TestInflightMessageLegacySnapshotIgnoresUnknownPropertyKeys(t *testing.T) {
	const future = `{"PacketID":1,"QoS":2,"Topic":"x","Payload":"","Retain":false,
		"Properties":{"ContentType":"text/plain","SomethingAddedLater":true},"ExpiresAt":"2030-01-02T03:04:05Z"}`

	var got InflightMessage
	if err := json.Unmarshal([]byte(future), &got); err != nil {
		t.Fatalf("a snapshot with unknown keys was rejected: %v", err)
	}
	if got.Properties.ContentType != "text/plain" {
		t.Errorf("ContentType = %q, want %q", got.Properties.ContentType, "text/plain")
	}
	if got.ExpiresAt.IsZero() {
		t.Error("ExpiresAt did not decode")
	}
}

// TestSessionDataPropertyFieldsAreAdditive documents the intended JSON shape so
// that a future rename of these fields is caught here instead of silently
// orphaning every persisted session.
func TestSessionDataPropertyFieldsAreAdditive(t *testing.T) {
	encoded, err := json.Marshal(&InflightMessage{
		Properties: MessageProperties{ContentType: "x"},
		ExpiresAt:  time.Unix(0, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	blob := string(encoded)
	for _, key := range []string{`"Properties"`, `"ExpiresAt"`, `"ContentType"`} {
		if !strings.Contains(blob, key) {
			t.Errorf("JSON %s is missing %s", blob, key)
		}
	}
}
