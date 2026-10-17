package integration

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestConnectionSlotsAreReusedAfterDisconnect pins the reservation accounting
// against maxConnections.
//
// reserveConnectionSlot takes a slot in pendingConns before CONNECT is
// processed, so that a burst of simultaneous connections cannot all observe the
// same count and all be admitted. Registration was supposed to consume that
// reservation, but it did not: a connected client stayed counted twice, once in
// len(connections) and once in pendingConns.
//
// Two consequences, both observable here. The effective limit was half of
// maxConnections -- a broker configured for 10000 refused the 5001st client.
// And the stale count never came back, so every connection the broker had ever
// served permanently consumed a slot: after enough reconnects a long-running
// broker would refuse everyone.
func TestConnectionSlotsAreReusedAfterDisconnect(t *testing.T) {
	const limit = 4

	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"

	b := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
		api.WithMaxConnections(limit),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(func() { b.Stop() })

	// fillAndDrain opens exactly `limit` connections, which must all succeed,
	// then closes them and gives the broker a moment to unregister.
	fillAndDrain := func(round string) {
		t.Helper()

		conns := make([]net.Conn, 0, limit)
		for i := 0; i < limit; i++ {
			c, err := net.DialTimeout("tcp", b.Addr(), 2*time.Second)
			if err != nil {
				t.Fatalf("%s round: dial %d of %d: %v", round, i+1, limit, err)
			}
			codec := protocol.NewCodec(0)
			// Distinct client IDs per round: a repeated ID would be treated as
			// a takeover rather than a new connection.
			connectClient(t, c, codec, fmt.Sprintf("%s-client-%d", round, i))
			conns = append(conns, c)
		}
		for _, c := range conns {
			_ = c.Close()
		}
		// Unregistration happens asynchronously when the read loop notices the
		// close. Long enough for the teardown path to run on a loaded CI box,
		// short enough that a stuck slot still fails the round quickly.
		time.Sleep(500 * time.Millisecond)
	}

	fillAndDrain("first")
	// Before the fix this round failed entirely: pendingConns still held the
	// first round's slots, so len(connections)+pendingConns was already at the
	// limit with zero connections registered.
	fillAndDrain("second")
	fillAndDrain("third")
}
