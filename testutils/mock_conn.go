// Package testutils provides mock implementations for testing shark-mqtt components.
package testutils

import (
	"io"
	"net"
	"sync"
	"time"
)

// MockConn implements net.Conn for testing.
type MockConn struct {
	ReadBuf  []byte
	WriteBuf []byte
	mu       sync.Mutex
	closed   bool
	readDeadline  time.Time
	writeDeadline time.Time
	localAddr     net.Addr
	remoteAddr    net.Addr
}

var _ net.Conn = (*MockConn)(nil)

// NewMockConn creates a new mock connection.
func NewMockConn() *MockConn {
	return &MockConn{
		localAddr:  &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1883},
		remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 50000},
	}
}

func (m *MockConn) Read(b []byte) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, io.EOF
	}
	if len(m.ReadBuf) == 0 {
		return 0, io.EOF
	}
	n = copy(b, m.ReadBuf)
	m.ReadBuf = m.ReadBuf[n:]
	return n, nil
}

func (m *MockConn) Write(b []byte) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, io.EOF
	}
	m.WriteBuf = append(m.WriteBuf, b...)
	return len(b), nil
}

func (m *MockConn) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *MockConn) LocalAddr() net.Addr  { return m.localAddr }
func (m *MockConn) RemoteAddr() net.Addr { return m.remoteAddr }

func (m *MockConn) SetDeadline(t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readDeadline = t
	m.writeDeadline = t
	return nil
}

func (m *MockConn) SetReadDeadline(t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readDeadline = t
	return nil
}

func (m *MockConn) SetWriteDeadline(t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeDeadline = t
	return nil
}

// WrittenData returns a copy of all data written to the mock connection.
func (m *MockConn) WrittenData() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]byte, len(m.WriteBuf))
	copy(out, m.WriteBuf)
	return out
}

// ClearWritten clears the write buffer.
func (m *MockConn) ClearWritten() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WriteBuf = nil
}

// SetReadData sets data to be returned on next Read calls.
func (m *MockConn) SetReadData(data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ReadBuf = data
}

// IsClosed returns whether the connection has been closed.
func (m *MockConn) IsClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}
