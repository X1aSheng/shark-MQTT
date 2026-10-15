package broker

// Read-only introspection used by the management API, plus the two explicit
// disconnect entry points an operator can call.

import "time"

type ClientInfo struct {
	ClientID string `json:"client_id"`
	Username string `json:"username,omitempty"`
	Online   bool   `json:"online"`
	// ProtocolVersion is the MQTT version byte: 3=3.1, 4=3.1.1, 5=5.0.
	ProtocolVersion uint8     `json:"protocol_version"`
	CleanStart      bool      `json:"clean_start"`
	KeepAlive       uint16    `json:"keep_alive"`
	ConnectedAt     time.Time `json:"connected_at"`
	LastActivity    time.Time `json:"last_activity"`
	// ExpiryInterval is the negotiated Session Expiry Interval: 0 means the
	// session ends with the connection.
	ExpiryInterval int `json:"session_expiry_interval"`
	// Subscriptions maps topic filter to granted QoS.
	Subscriptions map[string]uint8 `json:"subscriptions,omitempty"`
	// Inflight is the number of outbound QoS 1/2 messages awaiting acknowledgment.
	Inflight int `json:"inflight"`
}

func (b *Broker) Clients() []ClientInfo {
	b.mu.RLock()
	online := make(map[string]struct{}, len(b.connections))
	for id := range b.connections {
		online[id] = struct{}{}
	}
	b.mu.RUnlock()

	ids := b.sessions.ListSessions()
	out := make([]ClientInfo, 0, len(ids))
	for _, id := range ids {
		sess, ok := b.sessions.GetSession(id)
		if !ok {
			continue
		}
		info := b.clientInfo(sess)
		if _, isOnline := online[id]; isOnline {
			info.Online = true
		}
		out = append(out, info)
	}
	return out
}

func (b *Broker) Client(clientID string) (ClientInfo, bool) {
	sess, ok := b.sessions.GetSession(clientID)
	if !ok {
		return ClientInfo{}, false
	}
	info := b.clientInfo(sess)
	b.mu.RLock()
	_, info.Online = b.connections[clientID]
	b.mu.RUnlock()
	return info, true
}

func (b *Broker) clientInfo(sess *Session) ClientInfo {
	sess.mu.RLock()
	defer sess.mu.RUnlock()

	info := ClientInfo{
		ClientID:        sess.ClientID,
		Username:        sess.Username,
		ProtocolVersion: sess.ProtocolVer,
		CleanStart:      sess.IsClean,
		KeepAlive:       sess.KeepAlive,
		ConnectedAt:     sess.ConnectedAt,
		LastActivity:    sess.LastActivity,
		ExpiryInterval:  int(sess.ExpiryInterval),
		Inflight:        len(sess.Inflight) + len(sess.inboundQoS2),
	}
	if len(sess.Subscriptions) > 0 {
		info.Subscriptions = make(map[string]uint8, len(sess.Subscriptions))
		for filter, qos := range sess.Subscriptions {
			info.Subscriptions[filter] = qos
		}
	}
	return info
}

func (b *Broker) TopicStats() []TopicFilterStat {
	return b.topics.Stats()
}

func (b *Broker) DisconnectClient(clientID string) bool {
	b.mu.RLock()
	cs, ok := b.connections[clientID]
	b.mu.RUnlock()
	if !ok || cs == nil || cs.conn == nil {
		return false
	}

	// Suppress the will before closing: once the socket is down the readLoop
	// cannot tell an operator eviction from a client failure.
	b.will.RemoveWill(clientID)

	if err := cs.conn.Close(); err != nil {
		// The readLoop still wakes up and tears the session down; report only.
		b.logger.Debug("failed to close client connection on disconnect request",
			"clientID", clientID, "error", err)
	}
	return true
}

func (b *Broker) DisconnectAllClients() int {
	b.mu.RLock()
	ids := make([]string, 0, len(b.connections))
	for id := range b.connections {
		ids = append(ids, id)
	}
	b.mu.RUnlock()

	n := 0
	for _, id := range ids {
		if b.DisconnectClient(id) {
			n++
		}
	}
	return n
}
