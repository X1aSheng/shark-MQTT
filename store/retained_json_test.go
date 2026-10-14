package store

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

// store/badger and store/redis persist a whole RetainedMessage with
// encoding/json, so its layout is a durable format just like InflightMessage's.
// These tests pin the MQTT 5 relay fields added for §3.3.2.3 / §3.3.2.3.3 and
// the additive-only rule that keeps old snapshots loadable.

func TestRetainedMessageJSONRoundTrip(t *testing.T) {
	pfi := byte(1)
	sid := uint32(11)
	deadline := time.Date(2026, 10, 13, 4, 5, 6, 789000000, time.UTC)

	msg := &RetainedMessage{
		Topic:     "retained/round-trip",
		QoS:       1,
		Payload:   []byte("21.5"),
		Timestamp: time.Date(2026, 10, 13, 4, 0, 0, 0, time.UTC),
		Properties: MessageProperties{
			PayloadFormat:   &pfi,
			ContentType:     "text/plain",
			ResponseTopic:   "retained/reply",
			CorrelationData: []byte{0x01, 0x02},
			UserProperties: []UserProperty{
				{Key: "trace", Value: "x"},
				{Key: "trace", Value: "y"},
			},
			SubscriptionIdentifier: []uint32{sid},
		},
		ExpiresAt: deadline,
	}

	encoded, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal retained: %v", err)
	}
	var got RetainedMessage
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal retained: %v", err)
	}

	if got.Topic != msg.Topic || got.QoS != msg.QoS || string(got.Payload) != "21.5" {
		t.Errorf("core fields drifted: %+v", got)
	}
	if !got.Timestamp.Equal(msg.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, msg.Timestamp)
	}
	if !got.ExpiresAt.Equal(deadline) {
		// The absolute deadline has to survive, otherwise the retained copy's
		// remaining lifetime resets on every broker restart.
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, deadline)
	}
	if got.Properties.PayloadFormat == nil || *got.Properties.PayloadFormat != 1 {
		t.Errorf("PayloadFormat = %v, want pointer to 1", got.Properties.PayloadFormat)
	}
	if got.Properties.ContentType != "text/plain" {
		t.Errorf("ContentType = %q", got.Properties.ContentType)
	}
	if got.Properties.ResponseTopic != "retained/reply" {
		t.Errorf("ResponseTopic = %q", got.Properties.ResponseTopic)
	}
	if string(got.Properties.CorrelationData) != string([]byte{0x01, 0x02}) {
		t.Errorf("CorrelationData = %v, want [1 2]", got.Properties.CorrelationData)
	}
	if len(got.Properties.UserProperties) != 2 ||
		got.Properties.UserProperties[0] != (UserProperty{Key: "trace", Value: "x"}) ||
		got.Properties.UserProperties[1] != (UserProperty{Key: "trace", Value: "y"}) {
		t.Errorf("UserProperties = %+v, want trace=x then trace=y in order", got.Properties.UserProperties)
	}
	if !slices.Equal(got.Properties.SubscriptionIdentifier, []uint32{11}) {
		t.Errorf("SubscriptionIdentifier = %v, want [11]", got.Properties.SubscriptionIdentifier)
	}
}

// TestRetainedMessageLegacyJSONStillLoads pins the backward-compatibility half:
// a retained record written before the relay fields existed still decodes.
func TestRetainedMessageLegacyJSONStillLoads(t *testing.T) {
	const legacy = `{"Topic":"retained/legacy","QoS":1,"Payload":"aGk=","Timestamp":"2026-01-02T03:04:05Z"}`

	var got RetainedMessage
	if err := json.Unmarshal([]byte(legacy), &got); err != nil {
		t.Fatalf("a pre-existing retained snapshot no longer decodes: %v", err)
	}
	if got.Topic != "retained/legacy" || string(got.Payload) != "hi" {
		t.Errorf("legacy fields drifted: %+v", got)
	}
	if got.Properties.PayloadFormat != nil || got.Properties.ContentType != "" ||
		got.Properties.ResponseTopic != "" || got.Properties.CorrelationData != nil ||
		got.Properties.UserProperties != nil || len(got.Properties.SubscriptionIdentifier) != 0 {
		t.Errorf("Properties = %+v, want the zero value", got.Properties)
	}
	if !got.ExpiresAt.IsZero() {
		// A legacy record has no publisher deadline; treating it as expired
		// would drop every retained message on upgrade.
		t.Errorf("ExpiresAt = %v, want the zero time", got.ExpiresAt)
	}
}

// TestRetainedMessageJSONPreservesNilSlices mirrors the inflight case: the
// broker decides whether a property can be replayed at all by testing these
// fields against nil, so JSON must not turn nil into empty or the reverse.
func TestRetainedMessageJSONPreservesNilSlices(t *testing.T) {
	encoded, err := json.Marshal(&RetainedMessage{Topic: "t", Payload: []byte("p")})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got RetainedMessage
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
}
