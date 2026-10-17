package main

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// runIdle opens n MQTT connections, completes the CONNECT/CONNACK exchange and
// then holds them open without publishing anything.
//
// It answers a different question than the load mode: not "how fast can the
// broker move messages" but "what does an idle device fleet cost in memory and
// threads". The connections are raw TCP plus the project codec rather than
// client.Client, so what is measured is the broker's per-connection cost, not
// the test client's.
//
// KeepAlive is 0, so the broker holds these connections indefinitely: the point
// is to observe steady-state footprint, not to exercise keep-alive handling.
func runIdle(addr string, n int, hold time.Duration, concurrency int) {
	if concurrency < 1 {
		concurrency = 64
	}

	var ok, fail atomic.Int64
	var firstErr atomic.Value
	var mu sync.Mutex
	conns := make([]net.Conn, 0, n)

	start := time.Now()
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(id int) {
			defer wg.Done()
			defer func() { <-sem }()
			c, err := dialAndConnect(addr, id)
			if err != nil {
				fail.Add(1)
				// Keep the first failure verbatim: "connection refused", "only
				// one usage of each socket address" and "i/o timeout" point at
				// three completely different limits, and a bare failure count
				// cannot tell them apart.
				firstErr.CompareAndSwap(nil, err.Error())
				return
			}
			ok.Add(1)
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	rate := 0.0
	if elapsed > 0 {
		rate = float64(ok.Load()) / elapsed.Seconds()
	}
	fmt.Printf("idle mode: %d connections established in %v (%.0f conn/s), %d failed\n",
		ok.Load(), elapsed.Round(time.Millisecond), rate, fail.Load())
	if s, _ := firstErr.Load().(string); s != "" {
		fmt.Printf("first failure: %s\n", s)
	}

	if hold > 0 && ok.Load() > 0 {
		fmt.Printf("holding %d connections for %v; sample the broker now...\n",
			ok.Load(), hold.Round(time.Second))
		time.Sleep(hold)
	}

	for _, c := range conns {
		_ = c.Close()
	}
	fmt.Printf("closed %d connections\n", len(conns))
}

// dialAndConnect performs one CONNECT/CONNACK exchange and returns the live
// connection with its deadline cleared.
//
// The source address is spread over a block of loopback addresses. A single
// 127.0.0.1 gives the client only the 16384 ports of the Windows dynamic range,
// and the TIME_WAIT entries left by one run starve the next: a ten-thousand
// connection test from one source address ends up measuring the port table
// instead of the broker. The whole 127/8 block is loopback on both Windows and
// Linux, so rotating over it multiplies the available port space.
func dialAndConnect(addr string, id int) (net.Conn, error) {
	d := net.Dialer{
		Timeout:   10 * time.Second,
		LocalAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, byte(2+id%16))},
	}
	c, err := d.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}

	codec := protocol.NewCodec(0)
	if err := c.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		c.Close()
		return nil, err
	}

	connect := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       0,
		ClientID:        fmt.Sprintf("idle-%d", id),
	}
	if err := codec.Encode(c, connect); err != nil {
		c.Close()
		return nil, err
	}
	if _, err := codec.Decode(c); err != nil {
		c.Close()
		return nil, err
	}

	if err := c.SetDeadline(time.Time{}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}
