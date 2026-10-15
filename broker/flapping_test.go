package broker

import (
	"testing"
	"time"
)

// fakeClock drives the detector without sleeping, so the window and penalty
// tests are deterministic and fast.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestDetector(cfg FlappingConfig) (*FlappingDetector, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	d := NewFlappingDetector(cfg)
	d.Stop() // no cleanup goroutine: the tests call Cleanup explicitly
	d.SetClock(clk.now)
	return d, clk
}

// TestFlapping_DisabledByDefault: a broker whose clients legitimately reconnect
// often must not be silently throttled by a wrong limit.
func TestFlapping_DisabledByDefault(t *testing.T) {
	d, _ := newTestDetector(FlappingConfig{MaxConnects: 1})
	for i := 0; i < 100; i++ {
		if got := d.Check("10.0.0.1", "c"); got != FlapAllow {
			t.Fatalf("attempt %d = %v, want allow with detection disabled", i, got)
		}
	}
}

// TestFlapping_TripsThenPenalizes is the core behaviour.
func TestFlapping_TripsThenPenalizes(t *testing.T) {
	d, _ := newTestDetector(FlappingConfig{
		Enabled:         true,
		MaxConnects:     3,
		SlidingWindow:   10 * time.Second,
		PenaltyDuration: 60 * time.Second,
	})

	for i := 0; i < 3; i++ {
		if got := d.Check("10.0.0.1", "c"); got != FlapAllow {
			t.Fatalf("attempt %d = %v, want allow", i, got)
		}
	}
	if got := d.Check("10.0.0.1", "c"); got != FlapExceeded {
		t.Fatalf("4th attempt = %v, want FlapExceeded", got)
	}
	// Penalized from here on, without further recording.
	if got := d.Check("10.0.0.1", "c"); got != FlapRejected {
		t.Fatalf("5th attempt = %v, want FlapRejected", got)
	}
	if remaining, penalized := d.PenaltyRemaining("c"); !penalized || remaining <= 0 {
		t.Errorf("PenaltyRemaining = %v/%v, want a positive penalty", remaining, penalized)
	}
}

// TestFlapping_PenaltyExpires covers automatic unblocking.
func TestFlapping_PenaltyExpires(t *testing.T) {
	d, clk := newTestDetector(FlappingConfig{
		Enabled:         true,
		MaxConnects:     2,
		SlidingWindow:   10 * time.Second,
		PenaltyDuration: 30 * time.Second,
	})

	d.Check("ip", "c")
	d.Check("ip", "c")
	if got := d.Check("ip", "c"); got != FlapExceeded {
		t.Fatalf("3rd attempt = %v, want FlapExceeded", got)
	}

	clk.advance(29 * time.Second)
	if got := d.Check("ip", "c"); got != FlapRejected {
		t.Errorf("at 29s = %v, want still rejected", got)
	}

	clk.advance(2 * time.Second) // now 31s > 30s penalty
	if got := d.Check("ip", "c"); got != FlapAllow {
		t.Errorf("after penalty expiry = %v, want allow", got)
	}
}

// TestFlapping_PenaltyNotExtendedByRetries is a design decision worth pinning.
//
// If a retry pushed the expiry forward, a client stuck in a reconnect storm
// could never recover: every attempt would postpone its own release forever, so
// the broker would permanently lock out exactly the clients it is trying to
// protect itself from.
func TestFlapping_PenaltyNotExtendedByRetries(t *testing.T) {
	d, clk := newTestDetector(FlappingConfig{
		Enabled:         true,
		MaxConnects:     1,
		SlidingWindow:   10 * time.Second,
		PenaltyDuration: 30 * time.Second,
	})

	d.Check("ip", "c")
	if got := d.Check("ip", "c"); got != FlapExceeded {
		t.Fatalf("2nd attempt = %v, want FlapExceeded", got)
	}
	firstExpiry, _ := d.PenaltyRemaining("c")

	// Hammer it throughout the penalty window, advancing in steps small enough
	// that every attempt lands while the penalty is still active.
	for i := 0; i < 10; i++ {
		clk.advance(2 * time.Second)
		if got := d.Check("ip", "c"); got != FlapRejected {
			t.Fatalf("retry at +%ds = %v, want still rejected", (i+1)*2, got)
		}
		if remaining, _ := d.PenaltyRemaining("c"); remaining > firstExpiry {
			t.Fatalf("penalty grew from %v to %v; a retry extended it",
				firstExpiry, remaining)
		}
	}

	// At +20s the original 30s penalty is still running.
	clk.advance(11 * time.Second) // +31s
	if remaining, penalized := d.PenaltyRemaining("c"); penalized && remaining > 0 {
		t.Fatalf("still penalized %v after the nominal penalty elapsed", remaining)
	}
}

// TestFlapping_RetriableAfterPenalty shows a penalized client is not locked out
// permanently: once the penalty ends it may reconnect (though it starts a fresh
// window, so a continuing storm trips again -- which is the intent).
func TestFlapping_RetriableAfterPenalty(t *testing.T) {
	d, clk := newTestDetector(FlappingConfig{
		Enabled:         true,
		MaxConnects:     1,
		SlidingWindow:   10 * time.Second,
		PenaltyDuration: 30 * time.Second,
	})

	d.Check("ip", "c")
	d.Check("ip", "c") // trips

	clk.advance(31 * time.Second)
	if got := d.Check("ip", "c"); got != FlapAllow {
		t.Errorf("after penalty = %v, want allow", got)
	}
}

// TestFlapping_WindowSlides is why this is a sliding window rather than a
// counter reset by a timer: the budget frees up per elapsed interval, not on a
// fixed schedule.
func TestFlapping_WindowSlides(t *testing.T) {
	d, clk := newTestDetector(FlappingConfig{
		Enabled:         true,
		MaxConnects:     3,
		SlidingWindow:   10 * time.Second,
		PenaltyDuration: 60 * time.Second,
	})

	// Two attempts, 4s apart. Timestamps land at t=0 and t=4.
	d.Check("ip", "c")
	clk.advance(4 * time.Second)
	d.Check("ip", "c")

	// Advance past the window so both earlier stamps expire: at t=15, t=0 and
	// t=4 are both outside (15-4 = 11 > 10). The budget is fully restored,
	// which is the point of a sliding window.
	clk.advance(11 * time.Second)
	for i := 0; i < 3; i++ {
		if got := d.Check("ip", "c"); got != FlapAllow {
			t.Fatalf("post-slide attempt %d = %v, want allow (budget must be restored)", i, got)
		}
	}
	// A fourth within the new window trips it: the budget resets, it does not
	// become unlimited.
	if got := d.Check("ip", "c"); got != FlapExceeded {
		t.Errorf("4th post-slide attempt = %v, want FlapExceeded", got)
	}
}

// TestFlapping_PortChangesDoNotBypass is the case a naive implementation gets
// wrong: keying on "ip:port" means a client reconnecting from a fresh ephemeral
// port every time never trips the limit at all.
func TestFlapping_PortChangesDoNotBypass(t *testing.T) {
	d, _ := newTestDetector(FlappingConfig{
		Enabled:         true,
		MaxConnects:     3,
		SlidingWindow:   10 * time.Second,
		PenaltyDuration: 60 * time.Second,
	})

	tripped := false
	for port := 40000; port < 40010; port++ {
		addr := "203.0.113.7:" + itoa(port)
		got := d.Check(peerIP(addr), "c-"+itoa(port))
		if got != FlapAllow {
			tripped = true
			break
		}
	}
	if !tripped {
		t.Fatal("varying the source port bypassed the limit entirely")
	}

	// And peerIP must actually strip the port.
	if got := peerIP("192.168.1.5:54321"); got != "192.168.1.5" {
		t.Errorf("peerIP = %q, want 192.168.1.5", got)
	}
	// A bare address must pass through unchanged.
	if got := peerIP("192.168.1.5"); got != "192.168.1.5" {
		t.Errorf("peerIP(bare) = %q, want 192.168.1.5", got)
	}
}

// TestFlapping_ClientIDChangeDoesNotBypass is the mirror image: keying only on
// the address lets a client dodge the limit by changing its Client ID.
func TestFlapping_ClientIDChangeDoesNotBypass(t *testing.T) {
	d, _ := newTestDetector(FlappingConfig{
		Enabled:         true,
		MaxConnects:     3,
		SlidingWindow:   10 * time.Second,
		PenaltyDuration: 60 * time.Second,
	})

	tripped := false
	for i := 0; i < 10; i++ {
		got := d.Check("198.51.100.9", "client-"+itoa(i))
		if got != FlapAllow {
			tripped = true
			break
		}
	}
	if !tripped {
		t.Fatal("varying the client ID bypassed the limit entirely")
	}
}

// TestFlapping_DistinctClientsAreIndependent guards the opposite failure: one
// noisy client must not penalize everyone else.
func TestFlapping_DistinctClientsAreIndependent(t *testing.T) {
	d, _ := newTestDetector(FlappingConfig{
		Enabled:         true,
		MaxConnects:     2,
		SlidingWindow:   10 * time.Second,
		PenaltyDuration: 60 * time.Second,
	})

	for i := 0; i < 5; i++ {
		d.Check("10.0.0.1", "noisy")
	}
	if got := d.Check("10.0.0.1", "noisy"); got == FlapAllow {
		t.Fatal("the noisy client was not limited")
	}

	for i := 0; i < 2; i++ {
		if got := d.Check("10.0.0.2", "quiet"); got != FlapAllow {
			t.Errorf("quiet client attempt %d = %v, want allow", i, got)
		}
	}
}

// TestFlapping_CleanupReclaimsMemory: entries for departed clients must not
// accumulate for the process lifetime.
func TestFlapping_CleanupReclaimsMemory(t *testing.T) {
	d, clk := newTestDetector(FlappingConfig{
		Enabled:         true,
		MaxConnects:     100,
		SlidingWindow:   10 * time.Second,
		PenaltyDuration: 30 * time.Second,
	})

	for i := 0; i < 500; i++ {
		d.Check("ip-"+itoa(i), "c-"+itoa(i))
	}
	// Each connection is tracked under two keys (address and client ID), so
	// 500 connections occupy 1000 entries.
	if got := d.Tracked(); got != 1000 {
		t.Fatalf("Tracked = %d, want 1000", got)
	}

	clk.advance(20 * time.Second)
	d.Cleanup()
	if got := d.Tracked(); got != 0 {
		t.Errorf("Tracked after cleanup = %d, want 0", got)
	}
}

// TestFlapping_MaxTrackedBoundsMemory covers the DoS guard itself.
func TestFlapping_MaxTrackedBoundsMemory(t *testing.T) {
	d, _ := newTestDetector(FlappingConfig{
		Enabled:           true,
		MaxConnects:       100,
		SlidingWindow:     10 * time.Second,
		PenaltyDuration:   30 * time.Second,
		MaxTrackedClients: 50,
	})

	for i := 0; i < 500; i++ {
		d.Check("ip-"+itoa(i), "c-"+itoa(i))
		if got := d.Tracked(); got > 50 {
			t.Fatalf("Tracked = %d after %d clients, want <= 50", got, i)
		}
	}
}

// TestFlapping_ResetLiftsPenaltyImmediately: an operator must be able to unblock
// a client without waiting out the penalty.
func TestFlapping_ResetLiftsPenaltyImmediately(t *testing.T) {
	d, _ := newTestDetector(FlappingConfig{
		Enabled:         true,
		MaxConnects:     1,
		SlidingWindow:   10 * time.Second,
		PenaltyDuration: 3600 * time.Second,
	})

	d.Check("ip", "c")
	d.Check("ip", "c")
	if got := d.Check("ip", "c"); got == FlapAllow {
		t.Fatal("expected the client to be penalized")
	}

	d.Reset("c")
	if got := d.Check("ip", "c"); got != FlapAllow {
		t.Errorf("after Reset = %v, want allow", got)
	}
}

// TestFlapping_NilDetectorAllows makes the disabled path require no nil checks
// at the call site.
func TestFlapping_NilDetectorAllows(t *testing.T) {
	var d *FlappingDetector
	if got := d.Check("ip", "c"); got != FlapAllow {
		t.Errorf("nil detector = %v, want allow", got)
	}
	if got := d.Tracked(); got != 0 {
		t.Errorf("nil detector Tracked = %d, want 0", got)
	}
	d.Stop() // must not panic
	d.Reset("x")
}

// TestFlapping_DefaultsAreApplied pins the documented defaults.
func TestFlapping_DefaultsAreApplied(t *testing.T) {
	d := NewFlappingDetector(FlappingConfig{Enabled: true})
	defer d.Stop()
	if d.cfg.MaxConnects != 1 {
		t.Errorf("MaxConnects = %d, want 1", d.cfg.MaxConnects)
	}
	if d.cfg.SlidingWindow != 10*time.Second {
		t.Errorf("SlidingWindow = %v, want 10s", d.cfg.SlidingWindow)
	}
	if d.cfg.PenaltyDuration != 60*time.Second {
		t.Errorf("PenaltyDuration = %v, want 60s", d.cfg.PenaltyDuration)
	}
	if d.cfg.MaxTrackedClients != 10000 {
		t.Errorf("MaxTrackedClients = %d, want 10000", d.cfg.MaxTrackedClients)
	}
}

// TestFlapping_MaxConnectsNeverZero guards the "one per window" floor: a
// MaxConnects of 0 would reject the very first connection, which reads as a
// broker that refuses everyone.
func TestFlapping_MaxConnectsNeverZero(t *testing.T) {
	d, _ := newTestDetector(FlappingConfig{Enabled: true, MaxConnects: 0})
	if got := d.Check("ip", "c"); got != FlapAllow {
		t.Errorf("first attempt = %v, want allow", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
