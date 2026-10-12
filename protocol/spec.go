package protocol

// Version is the wire protocol version byte (3 = MQTT 3.1, 4 = MQTT 3.1.1,
// 5 = MQTT 5.0). It is declared as an alias of uint8 to avoid churn across the
// codebase (Session.ProtocolVer, store.SessionData.ProtocolVer, packet
// ProtocolVersion fields are all uint8); a defined type can be introduced in a
// later iteration without changing the Spec/semantics layer.
type Version = uint8

// Spec is the unified semantic/capability description of one protocol version.
//
// Its purpose is to collapse the scattered `== Version50` / `!= Version50`
// binary branches (which had no third state, so 3.1 silently fell through to
// the 3.1.1 behavior) into a single capability query. Callers should ask
// "does this version support X?" rather than compare a raw version value.
type Spec struct {
	Version Version // 3 / 4 / 5

	// —— handshake / identity ——
	Name           string // protocol name: "MQIsdp" (3.1) / "MQTT" (3.1.1, 5.0)
	AllowEmptyID   bool   // 3.1: false; 3.1.1 / 5.0: true (empty id requires Clean Session/Start = 1)
	MaxClientIDLen int    // 3.1: 23; 3.1.1 / 5.0: 0 (= only bounded by the server limit)

	// —— CONNACK semantics ——
	ConnAckSessionPresent bool // 3.1: false (the byte is reserved and must be 0); 3.1.1 / 5.0: true
	ConnAckHasReasonCode  bool // only 5.0: true; 3.1 / 3.1.1 return 0x00–0x05 return codes

	// —— SUBACK ——
	SubAckFailureSupported bool // 3.1: false (no 0x80 failure code); 3.1.1 / 5.0: true

	// —— packet capabilities (drive property / reason / packet support) ——
	Properties       bool // only 5.0
	ReasonCodes      bool // only 5.0 (PUBACK/PUBREC/PUBREL/PUBCOMP/DISCONNECT carry reason + properties)
	ServerDisconnect bool // only 5.0: the server may actively send a DISCONNECT
	TopicAlias       bool // only 5.0
	AuthPacket       bool // only 5.0 (enhanced authentication §4.12)
}

// specV31 returns the MQTT 3.1 capability set.
func specV31() Spec {
	return Spec{
		Version:        Version31,
		Name:           ProtocolNameMQIsdp,
		AllowEmptyID:   false,
		MaxClientIDLen: 23,
	}
}

// specV311 returns the MQTT 3.1.1 capability set.
func specV311() Spec {
	return Spec{
		Version:                Version311,
		Name:                   ProtocolNameMQTT,
		AllowEmptyID:           true,
		MaxClientIDLen:         0,
		ConnAckSessionPresent:  true,
		SubAckFailureSupported: true,
	}
}

// specV50 returns the MQTT 5.0 capability set.
func specV50() Spec {
	return Spec{
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
	}
}

// SpecOf returns the semantic description for a version. Unknown versions
// return ok = false and a zero Spec.
func SpecOf(v Version) (Spec, bool) {
	switch v {
	case Version31:
		return specV31(), true
	case Version311:
		return specV311(), true
	case Version50:
		return specV50(), true
	default:
		return Spec{}, false
	}
}
