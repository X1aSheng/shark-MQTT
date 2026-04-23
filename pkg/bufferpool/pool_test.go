package bufferpool

import (
	"testing"
)

func TestNewDefaultSize(t *testing.T) {
	p := New(0)
	if p == nil {
		t.Fatal("New(0) returned nil")
	}
	if p.size != 4096 {
		t.Errorf("expected size 4096 for New(0), got %d", p.size)
	}
}

func TestNewNegativeSize(t *testing.T) {
	p := New(-100)
	if p == nil {
		t.Fatal("New(-100) returned nil")
	}
	if p.size != 4096 {
		t.Errorf("expected size 4096 for New(-100), got %d", p.size)
	}
}

func TestNewExplicitSize(t *testing.T) {
	p := New(8192)
	if p.size != 8192 {
		t.Errorf("expected size 8192, got %d", p.size)
	}
}

func TestGetReturnsCorrectLength(t *testing.T) {
	p := New(4096)
	buf := p.Get()
	if len(buf) != 4096 {
		t.Errorf("expected Get() to return buffer of length 4096, got %d", len(buf))
	}
	if cap(buf) < 4096 {
		t.Errorf("expected Get() to return buffer with capacity >= 4096, got %d", cap(buf))
	}
}

func TestPutSmallBufferIgnored(t *testing.T) {
	p := New(4096)
	smallBuf := make([]byte, 100)
	// Put should silently ignore buffers with capacity less than pool size
	p.Put(smallBuf)
	// No panic - test passes
}

func TestPutNilBuffer(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Put(nil) panicked: %v", r)
		}
	}()
	p := New(4096)
	p.Put(nil)
}

func TestDefaultPool(t *testing.T) {
	p := Default()
	if p == nil {
		t.Fatal("Default() returned nil")
	}
	if p.size != 4096 {
		t.Errorf("expected default pool size 4096, got %d", p.size)
	}
}

func TestGetPutRoundTrip(t *testing.T) {
	p := New(1024)
	buf := p.Get()

	// Use the buffer
	for i := range buf {
		buf[i] = byte(i % 256)
	}

	// Put it back (capacity must be >= size)
	if cap(buf) >= 1024 {
		p.Put(buf)
	}

	// Get again - should not panic
	buf2 := p.Get()
	if len(buf2) != 1024 {
		t.Errorf("expected buffer length 1024, got %d", len(buf2))
	}
}

func TestMultipleGetPut(t *testing.T) {
	p := New(512)
	buffers := make([][]byte, 10)
	for i := range buffers {
		buffers[i] = p.Get()
		if len(buffers[i]) != 512 {
			t.Errorf("buffer %d: expected length 512, got %d", i, len(buffers[i]))
		}
	}
	for _, buf := range buffers {
		p.Put(buf)
	}
}

func TestPoolConcurrentAccess(t *testing.T) {
	p := New(256)
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				buf := p.Get()
				if len(buf) != 256 {
					t.Errorf("expected length 256, got %d", len(buf))
				}
				p.Put(buf)
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}
