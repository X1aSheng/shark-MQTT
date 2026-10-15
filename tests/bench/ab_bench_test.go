package bench

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/pkg/bufferpool"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// The comparisons below are A/B pairs: the same work with the tiered buffer pool
// enabled and disabled. They exist because "the pool reduces allocations" is a
// claim, and the interesting question is by how much, for which payload sizes,
// and whether it is worth the indirection at the small end.
//
// Each pair is reported as its own benchmark so benchstat lines them up; the
// comparison is meaningful because both sides run in the same binary on the same
// machine with the same -benchtime.

func benchCodecPool(b *testing.B, usePool bool, payloadSize int) {
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i)
	}

	c := protocol.NewCodec(1024 * 1024)
	var out bytes.Buffer
	if usePool {
		c.SetPool(bufferpool.Default())
	} else {
		c.SetPool(nil)
	}

	frame := encodePublishFrame(payload, payloadSize)

	b.ReportAllocs()
	b.SetBytes(int64(payloadSize))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Decode then re-encode: the round trip is what a broker actually does
		// per inbound packet, and it exercises both the pooled read and the
		// subsequent release.
		pkt, err := c.Decode(bytes.NewReader(frame))
		if err != nil {
			b.Fatalf("decode: %v", err)
		}
		out.Reset()
		if err := c.Encode(&out, pkt); err != nil {
			b.Fatalf("encode: %v", err)
		}
	}
}

// encodePublishFrame builds a wire-format PUBLISH for the given payload.
func encodePublishFrame(payload []byte, size int) []byte {
	topic := []byte("bench/pool")
	// fixed header + remaining length + topic + payload (QoS 0, no packet id)
	rl := 2 + len(topic) + size
	buf := make([]byte, 0, 1+2+rl)
	buf = append(buf, 0x30)
	buf = append(buf, encodeVarint(rl)...)
	buf = append(buf, byte(len(topic)>>8), byte(len(topic)))
	buf = append(buf, topic...)
	buf = append(buf, payload...)
	return buf
}

func encodeVarint(n int) []byte {
	var out []byte
	for {
		b := byte(n % 128)
		n /= 128
		if n > 0 {
			b |= 0x80
		}
		out = append(out, b)
		if n == 0 {
			return out
		}
	}
}

func BenchmarkPoolOff_64B(b *testing.B)  { benchCodecPool(b, false, 64) }
func BenchmarkPoolOn_64B(b *testing.B)   { benchCodecPool(b, true, 64) }
func BenchmarkPoolOff_256B(b *testing.B) { benchCodecPool(b, false, 256) }
func BenchmarkPoolOn_256B(b *testing.B)  { benchCodecPool(b, true, 256) }
func BenchmarkPoolOff_4KB(b *testing.B)  { benchCodecPool(b, false, 4096) }
func BenchmarkPoolOn_4KB(b *testing.B)   { benchCodecPool(b, true, 4096) }
func BenchmarkPoolOff_64KB(b *testing.B) { benchCodecPool(b, false, 64*1024) }
func BenchmarkPoolOn_64KB(b *testing.B)  { benchCodecPool(b, true, 64*1024) }

// BenchmarkTieredVsSinglePool isolates the contribution of tiering alone: the
// same 4KB request against a tiered pool (16KB bucket) and against a pool that
// only has a 4KB bucket, so the numbers differ only in how much is carried.
func BenchmarkTieredVsSinglePool_4KB(b *testing.B) {
	payload := make([]byte, 4096)
	frame := encodePublishFrame(payload, 4096)

	for _, tc := range []struct {
		name string
		pool *bufferpool.Pool
	}{
		{"tiered", bufferpool.New(512, 1024, 4096)},
		{"single-4k", bufferpool.New(4096)},
		{"none", nil},
	} {
		b.Run(tc.name, func(b *testing.B) {
			c := protocol.NewCodec(1024 * 1024)
			c.SetPool(tc.pool)
			var out bytes.Buffer
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pkt, err := c.Decode(bytes.NewReader(frame))
				if err != nil {
					b.Fatalf("decode: %v", err)
				}
				out.Reset()
				_ = c.Encode(&out, pkt)
			}
		})
	}
}

// BenchmarkTopicTreeFanout measures the cost of a publish against N subscribers,
// which is the shape that dominates a broker under real load: many sessions,
// each with a subscription, all matching one topic filter.
// sink keeps the compiler from eliding the Match call.
var sink []broker.Subscriber

func BenchmarkTopicTreeFanout(b *testing.B) {
	for _, subs := range []int{1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("subs-%d", subs), func(b *testing.B) {
			tree := broker.NewTopicTree()
			for i := 0; i < subs; i++ {
				tree.Subscribe("bench/fanout/topic", fmt.Sprintf("client-%d", i), 0)
			}
			// Match takes a topic string, not a packet: the PublishPacket is
			// built by the caller, so what this measures is the tree walk plus
			// the result slice allocation that fanout implies.
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sink = tree.Match("bench/fanout/topic")
			}
		})
	}
}

// BenchmarkWireRoundTrip measures the socket round trip the codec sits inside,
// so the codec numbers can be read as a fraction of the total cost rather than
// as the whole cost.
func BenchmarkWireRoundTrip(b *testing.B) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go io.Copy(io.Discard, c)
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()

	payload := make([]byte, 256)
	frame := encodePublishFrame(payload, len(payload))

	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := conn.Write(frame); err != nil {
			b.Fatal(err)
		}
	}
}
