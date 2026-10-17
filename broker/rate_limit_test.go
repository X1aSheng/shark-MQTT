package broker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConnRateLimiterAllowsFractionalRate pins the guard against a rate below
// one connection per window.
//
// The slot count used to be truncated (int(maxRate*windowSeconds)), so 0.5/s
// produced 0 and "len(timestamps) >= 0" rejected every connection: a limit
// meant to slow reconnects down instead refused all of them.
func TestConnRateLimiterAllowsFractionalRate(t *testing.T) {
	r := newConnRateLimiter(time.Second)
	r.SetRate(0.5)

	if !r.Allow() {
		t.Fatal("rate 0.5/s rejected the first connection in the window")
	}
	if r.Allow() {
		t.Fatal("rate 0.5/s admitted a second connection in the same window")
	}
}

// TestReserveConnectionSlotDoesNotOverAdmit pins the maxConnections admission
// path under concurrency.
//
// The old check sampled len(b.connections) and then spent the whole CONNECT
// path (authorization, session restore, store writes) before inserting, so a
// burst of concurrent CONNECTs all observed the same count and all passed. The
// reservation makes the limit hold: exactly maxConnections slots are granted,
// no matter how many goroutines race for them.
func TestReserveConnectionSlotDoesNotOverAdmit(t *testing.T) {
	const limit = 5
	b := New(WithMaxConnections(limit))

	const racers = 200
	var granted atomic.Int64
	start := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if b.reserveConnectionSlot() {
				granted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := granted.Load(); got != limit {
		t.Fatalf("granted %d connection slots, want exactly %d", got, limit)
	}
}

// TestReleaseConnectionSlotReturnsTheReservation is the other half: a CONNECT
// that fails after reserving must not keep shrinking the pool.
func TestReleaseConnectionSlotReturnsTheReservation(t *testing.T) {
	const limit = 2
	b := New(WithMaxConnections(limit))

	if !b.reserveConnectionSlot() || !b.reserveConnectionSlot() {
		t.Fatal("expected both slots to be available")
	}
	if b.reserveConnectionSlot() {
		t.Fatal("admitted a third connection over a limit of 2")
	}

	b.releaseConnectionSlot()
	if !b.reserveConnectionSlot() {
		t.Fatal("released slot was not returned to the pool")
	}
}

// TestReserveConnectionSlotUnlimited keeps the unlimited configuration free of
// bookkeeping: it must admit any number and release must stay a no-op.
func TestReserveConnectionSlotUnlimited(t *testing.T) {
	b := New() // no WithMaxConnections: unlimited
	for i := 0; i < 1000; i++ {
		if !b.reserveConnectionSlot() {
			t.Fatalf("unlimited broker rejected connection %d", i)
		}
	}
}
