// Package sharksocket provides an adapter to integrate shark-mqtt with shark-socket Gateway.
//
// Usage:
//
//	import (
//	    ss "github.com/yourorg/shark-socket/api"
//	    mqttadapter "github.com/X1aSheng/shark-mqtt/test/integration/sharksocket"
//	)
//
//	adapter := mqttadapter.New(":1883", cfg)
//	gw := ss.NewGateway().
//	    AddServer(adapter).
//	    Build()
package sharksocket

import (
	"context"

	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
)

// MQTTAdapter adapts shark-mqtt MQTTServer to a ProtocolServer interface
// for use with shark-socket Gateway.
type MQTTAdapter struct {
	srv    *broker.MQTTServer
	broker *broker.Broker
}

// New creates a new MQTTAdapter with the given config.
func New(cfg *config.Config) *MQTTAdapter {
	brk := broker.New()
	srv := broker.NewMQTTServer(cfg)
	srv.SetHandler(brk)
	return &MQTTAdapter{
		srv:    srv,
		broker: brk,
	}
}

// Protocol returns the protocol type for this adapter.
func (a *MQTTAdapter) Protocol() string {
	return "MQTT"
}

// Start begins listening for connections.
func (a *MQTTAdapter) Start() error {
	if err := a.broker.Start(); err != nil {
		return err
	}
	return a.srv.Start()
}

// Stop gracefully shuts down the adapter.
func (a *MQTTAdapter) Stop(ctx context.Context) error {
	a.srv.Stop()
	a.broker.Stop()
	return nil
}

// ActiveConnections returns the number of currently active MQTT connections.
func (a *MQTTAdapter) ActiveConnections() int64 {
	return a.srv.ConnCount()
}

// Broker returns the underlying Broker for advanced scenarios
// such as cross-protocol message routing.
func (a *MQTTAdapter) Broker() *broker.Broker {
	return a.broker
}
