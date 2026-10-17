package broker

// Per-connection outbound state: the write queue, its goroutine, and the
// classification of write failures.

import (
	"errors"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

func (cs *clientState) classifyWriteError(pkt protocol.Packet, err error) error {
	if err == nil || !errors.Is(err, protocol.ErrPacketTooLarge) {
		return err
	}
	if _, isConnAck := pkt.(*protocol.ConnAckPacket); isConnAck {
		return err
	}
	if cs.metrics != nil {
		cs.metrics.IncMessagesDropped("packet_too_large")
	}
	return nil
}

func (cs *clientState) writeOrEnqueue(pkt protocol.Packet) error {
	if cs.out == nil {
		cs.wmu.Lock()
		err := cs.codec.Encode(cs.conn, pkt)
		cs.wmu.Unlock()
		return cs.classifyWriteError(pkt, err)
	}
	select {
	case cs.out <- pkt:
	case <-cs.stopWrites:
	}
	return nil
}

// stopWriter closes stopWrites exactly once.
//
// stopWrites has several closers -- session takeover, connection teardown and
// broker Stop all reach it, and so does the writer goroutine itself when a
// write fails -- so a bare close() would panic the second time. Closing it is
// how producers learn the writer is gone: writeOrEnqueue and writeFinal both
// select on it.
func (cs *clientState) stopWriter() {
	cs.stopOnce.Do(func() {
		if cs.stopWrites != nil {
			close(cs.stopWrites)
		}
	})
}

func (cs *clientState) writeLoop() {
	// Every exit path must publish the stop signal, not just the shutdown one.
	// A failed write returned without closing stopWrites, so the queue was left
	// with no consumer and the channel open: writeOrEnqueue then blocked as soon
	// as the queue filled, and the callers that reach it -- the readLoop of the
	// publishing connection, the QoS retry republish and the will publisher --
	// stayed blocked for good. The retry republish runs on a single goroutine
	// for the whole broker, so one dead peer could stop retransmission for
	// every client.
	defer cs.stopWriter()
	for {
		select {
		case req := <-cs.syncWrites:
			// A writeFinal caller is blocked on done, so the result is reported
			// either way; the teardown that asked for the packet closes the
			// connection itself.
			req.done <- cs.writeOne(req.pkt, req.deadline)
		case pkt := <-cs.out:
			if err := cs.writeOne(pkt, cs.writeTimeout); err != nil {
				// Same zombie-connection guard as writePacket: a failed write
				// closes the socket so the reader notices and tears the
				// connection down promptly. The deferred stopWriter unblocks any
				// producer still trying to enqueue.
				cs.conn.Close()
				return
			}
		case <-cs.stopWrites:
			return
		}
	}
}

func (cs *clientState) writeOne(pkt protocol.Packet, deadline time.Duration) error {
	if deadline > 0 {
		_ = cs.conn.SetWriteDeadline(time.Now().Add(deadline))
	}
	encErr := cs.codec.Encode(cs.conn, pkt)
	if err := cs.classifyWriteError(pkt, encErr); err != nil {
		return err
	}
	if encErr != nil {
		// classifyWriteError swallows exactly one failure -- the codec refusing
		// an oversized packet -- so a non-nil encode error that survived it
		// means nothing reached the socket and there is nothing to flush.
		// Flushing anyway would push an empty frame on a transport that frames
		// each packet (WebSocket).
		return nil
	}
	// Transports that frame packets (WebSocket) flush the buffered packet as one
	// message after each Encode (R5).
	if f, ok := cs.conn.(packetFlusher); ok {
		return f.FlushPacket()
	}
	return nil
}

func (cs *clientState) writeFinal(pkt protocol.Packet) bool {
	if cs.syncWrites == nil {
		// clientStates built outside HandleConnection have no writer goroutine;
		// they serialize writes under wmu instead.
		cs.wmu.Lock()
		defer cs.wmu.Unlock()
		return cs.writeOne(pkt, finalWriteTimeout) == nil
	}

	req := syncWrite{pkt: pkt, deadline: finalWriteTimeout, done: make(chan error, 1)}

	// The wait outlasts the socket deadline the writer imposes from req, so a
	// write that was accepted but slow is not reported as never delivered.
	wait := finalWriteTimeout + 200*time.Millisecond
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case cs.syncWrites <- req:
	case <-cs.stopWrites:
		return false
	case <-timer.C:
		return false
	}

	select {
	case err := <-req.done:
		return err == nil
	case <-cs.stopWrites:
		return false
	case <-timer.C:
		return false
	}
}
