package client

import (
	"testing"
	"time"
)

func TestOptions(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		c := New()
		if c.opts.Host != "localhost" {
			t.Errorf("expected host localhost, got %s", c.opts.Host)
		}
		if c.opts.Port != 1883 {
			t.Errorf("expected port 1883, got %d", c.opts.Port)
		}
		if c.opts.KeepAlive != 60 {
			t.Errorf("expected keepalive 60, got %d", c.opts.KeepAlive)
		}
		if !c.opts.CleanSession {
			t.Error("expected clean session true")
		}
	})

	t.Run("WithHostPort", func(t *testing.T) {
		c := New(WithHostPort("broker.example.com", 8883))
		if c.opts.Host != "broker.example.com" {
			t.Errorf("expected host broker.example.com, got %s", c.opts.Host)
		}
		if c.opts.Port != 8883 {
			t.Errorf("expected port 8883, got %d", c.opts.Port)
		}
	})

	t.Run("WithAddr", func(t *testing.T) {
		c := New(WithAddr("10.0.0.1:1884"))
		if c.opts.Host != "10.0.0.1" {
			t.Errorf("expected host 10.0.0.1, got %s", c.opts.Host)
		}
		if c.opts.Port != 1884 {
			t.Errorf("expected port 1884, got %d", c.opts.Port)
		}
	})

	t.Run("WithClientID", func(t *testing.T) {
		c := New(WithClientID("test-client-123"))
		if c.opts.ClientID != "test-client-123" {
			t.Errorf("expected clientID test-client-123, got %s", c.opts.ClientID)
		}
	})

	t.Run("WithCredentials", func(t *testing.T) {
		c := New(WithCredentials("user", "pass"))
		if c.opts.Username != "user" {
			t.Errorf("expected username user, got %s", c.opts.Username)
		}
		if c.opts.Password != "pass" {
			t.Errorf("expected password pass, got %s", c.opts.Password)
		}
	})

	t.Run("WithKeepAlive", func(t *testing.T) {
		c := New(WithKeepAlive(30))
		if c.opts.KeepAlive != 30 {
			t.Errorf("expected keepalive 30, got %d", c.opts.KeepAlive)
		}
	})

	t.Run("WithCleanSession", func(t *testing.T) {
		c := New(WithCleanSession(false))
		if c.opts.CleanSession {
			t.Error("expected clean session false")
		}
	})

	t.Run("WithMaxPacketSize", func(t *testing.T) {
		c := New(WithMaxPacketSize(65536))
		if c.opts.MaxPacketSize != 65536 {
			t.Errorf("expected max packet size 65536, got %d", c.opts.MaxPacketSize)
		}
	})

	t.Run("WithConnectTimeout", func(t *testing.T) {
		c := New(WithConnectTimeout(10 * time.Second))
		if c.opts.ConnectTimeout != 10*time.Second {
			t.Errorf("expected connect timeout 10s, got %v", c.opts.ConnectTimeout)
		}
	})

	t.Run("Combined", func(t *testing.T) {
		c := New(
			WithHostPort("mqtt.example.com", 1883),
			WithClientID("my-client"),
			WithCredentials("admin", "secret"),
			WithKeepAlive(120),
			WithCleanSession(true),
			WithMaxPacketSize(1024*1024),
			WithConnectTimeout(15*time.Second),
		)
		if c.opts.Host != "mqtt.example.com" {
			t.Errorf("host: %s", c.opts.Host)
		}
		if c.opts.Port != 1883 {
			t.Errorf("port: %d", c.opts.Port)
		}
		if c.opts.ClientID != "my-client" {
			t.Errorf("clientID: %s", c.opts.ClientID)
		}
		if c.opts.Username != "admin" {
			t.Errorf("username: %s", c.opts.Username)
		}
		if c.opts.Password != "secret" {
			t.Errorf("password: %s", c.opts.Password)
		}
		if c.opts.KeepAlive != 120 {
			t.Errorf("keepalive: %d", c.opts.KeepAlive)
		}
		if !c.opts.CleanSession {
			t.Error("clean session not set")
		}
		if c.opts.MaxPacketSize != 1024*1024 {
			t.Errorf("maxPacketSize: %d", c.opts.MaxPacketSize)
		}
		if c.opts.ConnectTimeout != 15*time.Second {
			t.Errorf("connectTimeout: %v", c.opts.ConnectTimeout)
		}
	})
}

func TestIsConnected(t *testing.T) {
	c := New()
	if c.IsConnected() {
		t.Error("expected not connected initially")
	}
}

func TestSetOnMessage(t *testing.T) {
	c := New()
	called := false
	c.SetOnMessage(func(topic string, qos byte, payload []byte) {
		called = true
		if topic != "test/topic" {
			t.Errorf("expected topic test/topic, got %s", topic)
		}
		if qos != 1 {
			t.Errorf("expected qos 1, got %d", qos)
		}
		if string(payload) != "hello" {
			t.Errorf("expected payload hello, got %s", payload)
		}
	})
	// Simulate calling the callback
	c.msgMu.RLock()
	fn := c.onMessage
	c.msgMu.RUnlock()
	if fn != nil {
		fn("test/topic", 1, []byte("hello"))
	}
	if !called {
		t.Error("onMessage callback was not called")
	}
}

func TestNextPacketID(t *testing.T) {
	c := New()

	// Verify packet IDs increment and wrap correctly
	first := c.nextPacketID()
	if first != 1 {
		t.Errorf("expected first packet ID 1, got %d", first)
	}

	second := c.nextPacketID()
	if second != 2 {
		t.Errorf("expected second packet ID 2, got %d", second)
	}

	// Set nextPID near overflow and test wrap
	c.nextPID = 65535
	pid := c.nextPacketID()
	if pid != 65535 {
		t.Errorf("expected packet ID 65535, got %d", pid)
	}

	// Should wrap to 1
	pid = c.nextPacketID()
	if pid != 1 {
		t.Errorf("expected wrapped packet ID 1, got %d", pid)
	}
}

func TestConnectNotConnected(t *testing.T) {
	// Test that Publish returns error when not connected
	c := New(WithHostPort("127.0.0.1", 1883))
	c.ctx.Done() // ensure context is available

	err := c.Publish(c.ctx, "test", 0, false, []byte("data"))
	if err == nil {
		t.Error("expected error when publishing while not connected")
	}
}

func TestSubscribeNotConnected(t *testing.T) {
	c := New()
	_, err := c.Subscribe(c.ctx, []TopicSubscription{{Topic: "test", QoS: 0}})
	if err == nil {
		t.Error("expected error when subscribing while not connected")
	}
}

func TestUnsubscribeNotConnected(t *testing.T) {
	c := New()
	err := c.Unsubscribe(c.ctx, []string{"test"})
	if err == nil {
		t.Error("expected error when unsubscribing while not connected")
	}
}

func TestDisconnectWhenNotConnected(t *testing.T) {
	c := New()
	err := c.Disconnect(c.ctx)
	if err != nil {
		t.Errorf("expected no error when disconnecting while not connected, got %v", err)
	}
}
