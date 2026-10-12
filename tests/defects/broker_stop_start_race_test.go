package defects

import (
	"testing"

	"github.com/X1aSheng/shark-mqtt/broker"
)

// TestDefectBrokerStopStartLeavesNoRacingLoop is the regression guard for a
// data race found by `go test -race` on the documented Stop -> Start cycle
// (`broker.Start` explicitly supports it: "allow re-Start after Stop").
//
// Broker.Start launched sessionCleanupLoop and sysStatusLoop as untracked
// goroutines and Stop only cancelled the context, so a loop that was still
// draining a tick kept reading b.ctx (select on b.ctx.Done()) and b.startedAt
// ($SYS uptime) while the very next Start rewrote both fields:
//
//	WARNING: DATA RACE
//	Write at ... by broker.(*Broker).Start()          broker.go: b.ctx / b.startedAt
//	Previous read at ... by broker.(*Broker).sysStatusLoop()
//	                      broker.(*Broker).publishSystemStatus()
//
// The discriminating gate is `go test -race`: the loop below is a plain,
// deterministic exercise of the cycle (no sleeps, no timing assumptions) that
// reaches the race reliably. Without the fix this test fails with
// "race detected during execution of test"; with it, -race stays clean.
func TestDefectBrokerStopStartLeavesNoRacingLoop(t *testing.T) {
	brk := broker.New()
	for i := 0; i < 5; i++ {
		if err := brk.Start(); err != nil {
			t.Fatalf("start #%d: %v", i, err)
		}
		brk.Stop()
	}
}
