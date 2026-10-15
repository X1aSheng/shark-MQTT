package broker

import (
	"net"
	"sort"
	"sync"
	"time"
)

// FlappingConfig configures connection flapping detection.
//
// The threat is a client (or a script) that reconnects in a tight loop: each
// connection costs a goroutine, a file descriptor, a codec buffer and, if it
// authenticates, a session registration. A client that reconnects thousands of
// times a second can exhaust the broker without ever holding many concurrent
// connections, so a plain MaxConnections limit does not catch it.
//
// # What counts as flapping
//
// Within SlidingWindow, a client may open at most MaxConnects connections.
// Exceeding that puts the client into a penalty state for PenaltyDuration,
// during which every connection attempt is rejected without further work.
//
// # Identified by two keys
//
// Both the remote address and the client ID are tracked, because a client can
// vary either one:
//   - Varying the source port defeats an IP-only check. RemoteAddr must be
//     reduced to its IP, never the host:port.
//   - Varying the client ID defeats a clientID-only check.
//
// Hitting the limit on either key results in the penalty.
type FlappingConfig struct {
	// Enabled turns detection on. Off by default: a limit that rejects clients
	// silently is worse than no limit on a broker whose clients legitimately
	// reconnect often.
	Enabled bool

	// MaxConnects is how many connections one client may open within
	// SlidingWindow. Must be >= 1; values below are treated as 1.
	MaxConnects int

	// SlidingWindow is the period over which connections are counted.
	// Defaults to 10s.
	SlidingWindow time.Duration

	// PenaltyDuration is how long a client stays rejected after tripping the
	// limit. Defaults to 60s.
	//
	// The penalty is deliberately NOT extended on each further attempt: a
	// client stuck in a reconnect storm would otherwise never be able to
	// recover, because every attempt would push the expiry further out.
	PenaltyDuration time.Duration

	// MaxTrackedClients bounds memory. When exceeded, the least-recently-seen
	// clients are dropped. Defaults to 10000. Zero means use the default; a
	// negative value disables the bound, which is only safe when the number of
	// distinct clients is already known to be small.
	MaxTrackedClients int

	// CleanupInterval is how often expired entries are swept. Defaults to
	// SlidingWindow. Without it, entries for departed clients would live until
	// the process restarts.
	CleanupInterval time.Duration
}

func (c *FlappingConfig) applyDefaults() {
	if c.MaxConnects < 1 {
		c.MaxConnects = 1
	}
	if c.SlidingWindow <= 0 {
		c.SlidingWindow = 10 * time.Second
	}
	if c.PenaltyDuration <= 0 {
		c.PenaltyDuration = 60 * time.Second
	}
	if c.MaxTrackedClients == 0 {
		c.MaxTrackedClients = 10000
	}
	if c.CleanupInterval <= 0 {
		c.CleanupInterval = c.SlidingWindow
	}
}

// validator allows the detector to be exercised without pulling in the whole
// broker, and keeps the time source injectable so tests need not sleep.
type validator interface{ now() time.Time }

type realClock struct{}

func (realClock) now() time.Time { return time.Now() }

// FlappingDecision is the outcome of a check.
type FlappingDecision int

const (
	// FlapAllow means the connection may proceed.
	FlapAllow FlappingDecision = iota
	// FlapRejected means the client is in a penalty window.
	FlapRejected
	// FlapExceeded means this attempt tripped the limit and was rejected; the
	// penalty has now started.
	FlapExceeded
)

func (d FlappingDecision) String() string {
	switch d {
	case FlapRejected:
		return "rejected"
	case FlapExceeded:
		return "exceeded"
	default:
		return "allow"
	}
}

// flappingEntry is the per-key state: connection timestamps within the window,
// when the penalty began, and when the key was last touched (for LRU eviction).
type flappingEntry struct {
	// stamps is a small ring of recent connection times, oldest first. It is
	// sized to MaxConnects rather than the window: a client that stays under
	// the limit never needs more, and an over-limit client is rejected without
	// recording anyway.
	stamps []time.Time
	head   int

	penalizedUntil time.Time
	lastSeen       time.Time
	// alias links the two keys of one connection (address <-> client ID) so
	// Reset can clear both. Without it, lifting a penalty by client ID would
	// leave the address still penalized and the client would stay blocked --
	// Reset would appear not to work.
	alias []string
}

// FlappingDetector detects and throttles connection flapping.
//
// A nil *FlappingDetector is valid and never rejects, so callers need no nil
// checks when the feature is disabled.
type FlappingDetector struct {
	cfg   FlappingConfig
	clock validator
	mu    sync.Mutex
	keys  map[string]*flappingEntry
	// nowFn lets tests inject a clock. It is guarded by mu.
	stopOnce sync.Once
	stop     chan struct{}
}

// NewFlappingDetector creates a detector and starts its cleanup loop.
//
// Passing a nil config, or one with Enabled false, yields a detector that
// allows everything.
func NewFlappingDetector(cfg FlappingConfig) *FlappingDetector {
	cfg.applyDefaults()
	d := &FlappingDetector{
		cfg:   cfg,
		clock: realClock{},
		keys:  make(map[string]*flappingEntry),
		stop:  make(chan struct{}),
	}
	if cfg.Enabled {
		go d.cleanupLoop()
	}
	return d
}

// SetClock replaces the time source. Intended for tests; call before use.
func (d *FlappingDetector) SetClock(t func() time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clock = funcClock(t)
}

type funcClock func() time.Time

func (f funcClock) now() time.Time { return f() }

// Check records a connection attempt and reports what to do.
//
// ip must be the bare address (no port): pass peerIP, not peerAddr.
// clientID may be empty when it is not known yet, in which case only ip is
// tracked.
func (d *FlappingDetector) Check(ip, clientID string) FlappingDecision {
	if d == nil || !d.cfg.Enabled {
		return FlapAllow
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.clock.now()

	// An existing penalty short-circuits. Note this happens BEFORE recording:
	// a client in a penalty state must not extend it, and must not grow the
	// stamp slice on every retry.
	if key := ip; key != "" {
		if e, ok := d.keys[key]; ok && e.penalizedUntil.After(now) {
			e.lastSeen = now
			return FlapRejected
		}
	}
	if clientID != "" {
		if e, ok := d.keys[clientID]; ok && e.penalizedUntil.After(now) {
			e.lastSeen = now
			return FlapRejected
		}
	}

	// Record on BOTH keys, but count only the stricter result.
	//
	// Short-circuiting with || would record the IP but never the client ID when
	// the IP tripped, which is worse than it looks: a client that changes
	// source port gets a fresh address every time, so the IP key resets while
	// the client ID keeps the history -- the limit then never trips for it.
	// Recording both, then OR-ing the verdicts, keeps either key sufficient to
	// block while neither can be dodged.
	ipExceeded := d.record(ip, now)
	idExceeded := clientID != "" && d.record(clientID, now)
	d.link(ip, clientID)

	if ipExceeded || idExceeded {
		d.penalize(ip, now)
		d.penalize(clientID, now)
		return FlapExceeded
	}
	return FlapAllow
}

// record appends a timestamp and reports whether the limit is now exceeded.
func (d *FlappingDetector) record(key string, now time.Time) bool {
	if key == "" {
		return false
	}
	e, ok := d.keys[key]
	if !ok {
		if d.cfg.MaxTrackedClients > 0 && len(d.keys) >= d.cfg.MaxTrackedClients {
			d.evictOldestLocked()
		}
		e = &flappingEntry{stamps: make([]time.Time, 0, d.cfg.MaxConnects+1)}
		d.keys[key] = e
	}

	// Drop timestamps that fell out of the window.
	cutoff := now.Add(-d.cfg.SlidingWindow)
	for e.head < len(e.stamps) && e.stamps[e.head].Before(cutoff) {
		e.head++
	}
	if e.head > 0 {
		// Compact when the dead prefix dominates, so a long-lived entry does
		// not grow without bound.
		if e.head*2 >= len(e.stamps) {
			e.stamps = append(e.stamps[:0], e.stamps[e.head:]...)
			e.head = 0
		}
	}

	if len(e.stamps) < cap(e.stamps) {
		e.stamps = append(e.stamps, now)
	} else {
		// Should be unreachable (the limit trips first), but never grow past the
		// cap: an unbounded slice per key is a memory exhaustion vector itself.
		e.stamps[e.head] = now
	}

	e.lastSeen = now
	return len(e.stamps)-e.head > d.cfg.MaxConnects
}

// penalize puts a key into the penalty state and clears its window, so the
// client starts clean once the penalty expires.
func (d *FlappingDetector) penalize(key string, now time.Time) {
	if key == "" {
		return
	}
	e, ok := d.keys[key]
	if !ok {
		e = &flappingEntry{}
		d.keys[key] = e
	}
	e.penalizedUntil = now.Add(d.cfg.PenaltyDuration)
	e.stamps = e.stamps[:0]
	e.head = 0
	e.lastSeen = now
}

// evictOldestLocked drops the least-recently-seen quarter of the map.
//
// Evicting a single entry on every insert would make each insertion O(n) in the
// worst case. Evicting a batch amortizes it and keeps the bound approximate,
// which is all a memory guard needs. Caller must hold d.mu.
func (d *FlappingDetector) evictOldestLocked() {
	target := len(d.keys) / 4
	if target < 1 {
		target = 1
	}
	if target > len(d.keys) {
		target = len(d.keys)
	}
	type aged struct {
		key  string
		seen time.Time
	}
	candidates := make([]aged, 0, len(d.keys))
	for k, e := range d.keys {
		candidates = append(candidates, aged{k, e.lastSeen})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].seen.Before(candidates[j].seen)
	})
	for i := 0; i < target && i < len(candidates); i++ {
		delete(d.keys, candidates[i].key)
	}
}

// PenaltyRemaining reports how long key stays rejected, and whether it is
// penalized at all.
func (d *FlappingDetector) PenaltyRemaining(key string) (time.Duration, bool) {
	if d == nil || !d.cfg.Enabled || key == "" {
		return 0, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.keys[key]
	if !ok {
		return 0, false
	}
	remaining := e.penalizedUntil.Sub(d.clock.now())
	if remaining <= 0 {
		return 0, false
	}
	return remaining, true
}

// link records that two keys belonged to the same connection attempt, so
// Reset clears both. Caller must hold d.mu.
func (d *FlappingDetector) link(a, b string) {
	if a == "" || b == "" || a == b {
		return
	}
	ea, oka := d.keys[a]
	eb, okb := d.keys[b]
	if !oka || !okb {
		return
	}
	if !contains(ea.alias, b) {
		ea.alias = append(ea.alias, b)
	}
	if !contains(eb.alias, a) {
		eb.alias = append(eb.alias, a)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Reset clears all state for a key and any key it was seen alongside, lifting
// the penalty. Exposed so an operator can unblock a client without waiting.
func (d *FlappingDetector) Reset(key string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if e, ok := d.keys[key]; ok {
		for _, alias := range e.alias {
			delete(d.keys, alias)
		}
	}
	delete(d.keys, key)
}

// Tracked reports how many keys are currently held, for the status endpoint.
func (d *FlappingDetector) Tracked() int {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.keys)
}

// Stop ends the cleanup loop. Safe to call more than once.
func (d *FlappingDetector) Stop() {
	if d == nil {
		return
	}
	d.stopOnce.Do(func() { close(d.stop) })
}

func (d *FlappingDetector) cleanupLoop() {
	ticker := time.NewTicker(d.cfg.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-ticker.C:
			d.Cleanup()
		}
	}
}

// Cleanup removes entries whose window has expired and whose penalty has ended.
// Expired-then-dropped clients must not keep memory allocated forever.
func (d *FlappingDetector) Cleanup() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.clock.now()
	cutoff := now.Add(-d.cfg.SlidingWindow)
	for k, e := range d.keys {
		if !e.penalizedUntil.After(now) {
			alive := false
			for i := e.head; i < len(e.stamps); i++ {
				if !e.stamps[i].Before(cutoff) {
					alive = true
					break
				}
			}
			if !alive {
				delete(d.keys, k)
			}
		}
	}
}

// peerIP extracts the bare IP from a remote address.
//
// The port must be dropped: a client that reconnects from a new ephemeral port
// each time would defeat an address-string comparison entirely, which is the
// easiest possible way to slip past a flapping limit.
func peerIP(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// No port (or not host:port at all). Use the string as-is.
		return remoteAddr
	}
	return host
}
