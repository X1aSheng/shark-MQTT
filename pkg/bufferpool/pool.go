// Package bufferpool provides a sync.Pool for reusable byte buffers.
package bufferpool

import "sync"

// Pool is a buffer pool for reusable byte slices.
type Pool struct {
	pool sync.Pool
	size int
}

// New creates a new buffer pool with the specified buffer size.
func New(size int) *Pool {
	if size <= 0 {
		size = 4096
	}
	return &Pool{
		pool: sync.Pool{
			New: func() any {
				return make([]byte, size)
			},
		},
		size: size,
	}
}

// Get retrieves a buffer from the pool.
func (p *Pool) Get() []byte {
	return p.pool.Get().([]byte)
}

// Put returns a buffer to the pool.
func (p *Pool) Put(buf []byte) {
	if cap(buf) >= p.size {
		p.pool.Put(buf[:p.size])
	}
}

// Default returns a shared default buffer pool with 4KB buffers.
func Default() *Pool {
	return defaultPool
}

var defaultPool = New(4096)
