package protocol

import "testing"

// TestSpecOfSnapshots pins the capability table for each protocol version so a
// future edit to specV31/specV311/specV50 that silently changes the
// version-difference semantics fails loudly. These are exactly the fields the
// codec and broker core query to collapse the former scattered
// `== Version50` branches (see docs/architecture/PROTOCOL-ADAPTER-DESIGN.md
// §1.4 and §3).
func TestSpecOfSnapshots(t *testing.T) {
	tests := []struct {
		name    string
		version Version
		want    Spec
	}{
		{
			name:    "mqtt 3.1",
			version: Version31,
			want: Spec{
				Version:                Version31,
				Name:                   ProtocolNameMQIsdp,
				AllowEmptyID:           false,
				MaxClientIDLen:         23,
				ConnAckSessionPresent:  false,
				ConnAckHasReasonCode:   false,
				SubAckFailureSupported: false,
				Properties:             false,
				ReasonCodes:            false,
				ServerDisconnect:       false,
				TopicAlias:             false,
				AuthPacket:             false,
			},
		},
		{
			name:    "mqtt 3.1.1",
			version: Version311,
			want: Spec{
				Version:                Version311,
				Name:                   ProtocolNameMQTT,
				AllowEmptyID:           true,
				MaxClientIDLen:         0,
				ConnAckSessionPresent:  true,
				ConnAckHasReasonCode:   false,
				SubAckFailureSupported: true,
				Properties:             false,
				ReasonCodes:            false,
				ServerDisconnect:       false,
				TopicAlias:             false,
				AuthPacket:             false,
			},
		},
		{
			name:    "mqtt 5.0",
			version: Version50,
			want: Spec{
				Version:                Version50,
				Name:                   ProtocolNameMQTT,
				AllowEmptyID:           true,
				MaxClientIDLen:         0,
				ConnAckSessionPresent:  true,
				ConnAckHasReasonCode:   true,
				SubAckFailureSupported: true,
				Properties:             true,
				ReasonCodes:            true,
				ServerDisconnect:       true,
				TopicAlias:             true,
				AuthPacket:             true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := SpecOf(tt.version)
			if !ok {
				t.Fatalf("SpecOf(%d) ok = false, want true", tt.version)
			}
			if got != tt.want {
				t.Errorf("SpecOf(%d) mismatch:\n got = %+v\nwant = %+v", tt.version, got, tt.want)
			}
		})
	}
}

// TestSpecOfUnknownVersion documents that an unknown version yields ok=false
// together with a zero Spec; callers rely on this to fall back to the 3.1.1
// default rather than silently treating an unknown version as 5.0.
func TestSpecOfUnknownVersion(t *testing.T) {
	for _, v := range []Version{0, 1, 2, 6, 200, 255} {
		sp, ok := SpecOf(v)
		if ok {
			t.Errorf("SpecOf(%d) ok = true, want false", v)
		}
		if sp != (Spec{}) {
			t.Errorf("SpecOf(%d) = %+v, want zero Spec", v, sp)
		}
	}
}

// TestCodecSpecFollowsVersion verifies the codec exposes the capability table
// matching its negotiated version. The white-box path (setting protocolVersion
// directly, as several codec tests do) must still yield the correct Spec via
// the syncSpec self-heal, so this pins that contract.
func TestCodecSpecFollowsVersion(t *testing.T) {
	c := NewCodec(0)
	if got := c.Spec(); got.Version != Version311 {
		t.Fatalf("fresh codec Spec().Version = %d, want %d (default 3.1.1)", got.Version, Version311)
	}

	c.protocolVersion = Version50
	if got := c.Spec(); got.Version != Version50 || !got.Properties || !got.ReasonCodes {
		t.Fatalf("v5 codec Spec() = %+v, want Version50 with Properties/ReasonCodes", got)
	}

	c.protocolVersion = Version31
	got := c.Spec()
	if got.Version != Version31 {
		t.Fatalf("v3.1 codec Spec().Version = %d, want %d", got.Version, Version31)
	}
	if got.MaxClientIDLen != 23 || got.AllowEmptyID || got.ConnAckSessionPresent {
		t.Fatalf("v3.1 codec Spec() = %+v, want MaxClientIDLen=23, AllowEmptyID=false, ConnAckSessionPresent=false", got)
	}
}

// TestSpecComputeTimeInterfaceAssertions is a compile-time guarantee that the
// concrete codec satisfies the protocol-neutral PacketCodec contract.
func TestSpecComputeTimeInterfaceAssertions(t *testing.T) {
	var _ PacketCodec = (*Codec)(nil)
	var _ Adapter = (*MQTTAdapter)(nil)
}
