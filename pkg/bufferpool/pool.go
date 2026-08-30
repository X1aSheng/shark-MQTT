// Package bufferpool provides a tiered sync.Pool for reusable byte buffers.
//
// A single-size pool (the previous design) forces small messages to hold a
// large buffer and lets large messages keep growing — and then the pool pins
// those large backing arrays. A tiered pool instead selects the smallest
// bucket that fits a request, so a 20-byte topic reuses a 512B buffer while a
// 64KB payload reuses a 64KB buffer, without either wasting memory or pinning
// an oversized array.
package bufferpool

import (
	"sort"
	"sync"
)

// defaultSizes are the bucket sizes for the default pool (bytes).
var defaultSizes = []int{512, 1024, 2048, 16384, 32768, 65536, 262144}

// Pool is a tiered byte-buffer pool. Get(size) returns a slice whose length is
// exactly size and whose capacity is the smallest bucket that fits; Put
// reclaims a slice only when its capacity exactly matches a bucket.
type Pool struct {
	sizes []int
	pools []sync.Pool
}

// New creates a tiered pool. An empty (or all-non-positive) size list selects
// the default buckets; explicit sizes are sorted and de-duplicated.
func New(sizes ...int) *Pool {
	var ss []int
	for _, s := range sizes {
		if s > 0 {
			ss = append(ss, s)
		}
	}
	if len(ss) == 0 {
		ss = append(ss, defaultSizes...)
	}
	sort.Ints(ss)
	uniq := ss[:0]
	for i, s := range ss {
		if i == 0 || s != ss[i-1] {
			uniq = append(uniq, s)
		}
	}
	ss = uniq

	p := &Pool{sizes: ss, pools: make([]sync.Pool, len(ss))}
	for i, sz := range ss {
		sz := sz
		p.pools[i] = sync.Pool{
			New: func() any {
				b := make([]byte, sz)
				return &b
			},
		}
	}
	return p
}

// Get returns a slice of length size backed by the smallest bucket that fits.
// Requests larger than the biggest bucket allocate directly (and are not
// pooled), so the pool never pins an oversized array.
func (p *Pool) Get(size int) []byte {
	if size <= 0 {
		size = 1
	}
	for i, sz := range p.sizes {
		if size <= sz {
			buf := p.pools[i].Get().(*[]byte)
			return (*buf)[:size]
		}
	}
	return make([]byte, size)
}

// Put reclaims a slice whose capacity exactly matches a bucket. Slices whose
// capacity differs (e.g. grown by a caller, or allocated externally) are
// discarded, which keeps the pool from holding buffers of unexpected sizes.
func (p *Pool) Put(buf []byte) {
	if buf == nil {
		return
	}
	c := cap(buf)
	for i, sz := range p.sizes {
		if c == sz {
			p.pools[i].Put(&buf)
			return
		}
	}
}

// BufSize returns the largest bucket size (the threshold above which Get
// allocates directly instead of pooling).
func (p *Pool) BufSize() int {
	if len(p.sizes) == 0 {
		return 0
	}
	return p.sizes[len(p.sizes)-1]
}

// Default returns the shared default tiered pool.
func Default() *Pool {
	return defaultPool
}

var defaultPool = New()
