package bufferpool

import (
	"sync"
	"testing"
)

func TestNewDefaultTiers(t *testing.T) {
	p := New()
	if p == nil {
		t.Fatal("New() returned nil")
	}
	if got := len(p.sizes); got != len(defaultSizes) {
		t.Errorf("expected %d default tiers, got %d", len(defaultSizes), got)
	}
	if p.BufSize() != 262144 {
		t.Errorf("expected max bucket 262144, got %d", p.BufSize())
	}
}

func TestNewZeroFallsBackToDefault(t *testing.T) {
	p := New(0)
	if p == nil {
		t.Fatal("New(0) returned nil")
	}
	if p.BufSize() != 262144 {
		t.Errorf("expected default max bucket for New(0), got %d", p.BufSize())
	}
}

func TestNewExplicitTiers(t *testing.T) {
	p := New(2048, 512, 512, 1024) // unsorted with duplicates
	want := []int{512, 1024, 2048}
	if len(p.sizes) != len(want) {
		t.Fatalf("expected %d tiers, got %d", len(want), len(p.sizes))
	}
	for i, sz := range want {
		if p.sizes[i] != sz {
			t.Errorf("tier %d: want %d, got %d", i, sz, p.sizes[i])
		}
	}
}

func TestGetSelectsSmallestFittingBucket(t *testing.T) {
	p := New(512, 1024, 2048)
	cases := []struct {
		size    int
		wantLen int
		wantCap int
	}{
		{1, 1, 512},
		{512, 512, 512},
		{513, 513, 1024},
		{1024, 1024, 1024},
		{2048, 2048, 2048},
	}
	for _, c := range cases {
		buf := p.Get(c.size)
		if len(buf) != c.wantLen {
			t.Errorf("Get(%d): len=%d, want %d", c.size, len(buf), c.wantLen)
		}
		if cap(buf) != c.wantCap {
			t.Errorf("Get(%d): cap=%d, want %d", c.size, cap(buf), c.wantCap)
		}
		p.Put(buf)
	}
}

func TestGetOversizeAllocatesDirectly(t *testing.T) {
	p := New(512, 1024)
	buf := p.Get(4096)
	if len(buf) != 4096 || cap(buf) != 4096 {
		t.Errorf("expected direct 4096 allocation, got len=%d cap=%d", len(buf), cap(buf))
	}
	// Oversized buffer must not be pooled (capacity does not match a tier).
	p.Put(buf)
}

func TestPutReclaimsMatchingCapacityOnly(t *testing.T) {
	p := New(1024)
	// A slice with cap != a tier is discarded without panic.
	p.Put(make([]byte, 0, 2048)) // cap 2048 != tier 1024 -> dropped
	p.Put(nil)                   // nil -> no-op
}

func TestGetPutRoundTrip(t *testing.T) {
	p := New(1024)
	buf := p.Get(100)
	for i := range buf {
		buf[i] = byte(i % 256)
	}
	p.Put(buf)
	buf2 := p.Get(100)
	if len(buf2) != 100 || cap(buf2) != 1024 {
		t.Errorf("expected len=100 cap=1024 after round trip, got len=%d cap=%d", len(buf2), cap(buf2))
	}
}

func TestDefaultPool(t *testing.T) {
	p := Default()
	if p == nil {
		t.Fatal("Default() returned nil")
	}
	if p.BufSize() != 262144 {
		t.Errorf("expected default max bucket 262144, got %d", p.BufSize())
	}
}

func TestPoolConcurrentAccess(t *testing.T) {
	p := New(512, 1024, 2048)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				buf := p.Get(100)
				if len(buf) != 100 {
					t.Errorf("expected len 100, got %d", len(buf))
				}
				p.Put(buf)
			}
		}()
	}
	wg.Wait()
}

func TestBufSize(t *testing.T) {
	p := New(2048, 512)
	if size := p.BufSize(); size != 2048 {
		t.Errorf("BufSize() = %d, want 2048 (max tier)", size)
	}
}
