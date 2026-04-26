# Shark-MQTT Architecture Design Document v2.0

> Version: 2.0.0 | Last Updated: 2026-04-25 | Go Version: 1.26.1

---

## Table of Contents

1. [Project Overview](#project-overview)
2. [Relationship with Shark-Socket](#relationship-with-shark-socket)
3. [Design Principles](#design-principles)
4. [Layered Architecture](#layered-architecture)
5. [Directory Structure](#directory-structure)
6. [Core Interface Definitions](#core-interface-definitions)
7. [Network Layer](#network-layer)
8. [Protocol Layer](#protocol-layer)
9. [Broker Core Layer](#broker-core-layer)
10. [Session Management](#session-management)
11. [Storage Layer](#storage-layer)
12. [Authentication and Authorization](#authentication-and-authorization)
13. [Plugin Layer](#plugin-layer)
14. [API Layer](#api-layer)
15. [Configuration System](#configuration-system)
16. [Observability](#observability)
17. [Data Flow Design](#data-flow-design)
18. [Extension Guide](#extension-guide)
19. [Testing Strategy](#testing-strategy)
20. [Architecture Decision Records](#architecture-decision-records)

---

## Project Overview

Shark-MQTT is a standalone high-performance MQTT Broker implementation that supports **MQTT 3.1.1** and **MQTT 5.0** protocols, providing complete QoS 0/1/2, topic wildcards, persistent sessions, will messages, and retained message support.

Module path: `github.com/X1aSheng/shark-mqtt`

### Core Features

| Feature | Description |
|---------|-------------|
| Protocol Versions | MQTT 3.1.1 / 5.0 dual version support |
| QoS Levels | QoS 0 / 1 / 2 complete support |
| Persistent Sessions | CleanSession=false cross-connection session persistence |
| Topic Wildcards | `+` single-level, `#` multi-level wildcard matching |
| Will Messages | Will Message trigger mechanism |
| Retained Messages | Retained Message storage and delivery |
| Pluggable Storage | Memory / Redis / BadgerDB |
| Pluggable Auth | Interface-based authentication and authorization |
| Plugin System | Hook-based plugin system (OnAccept, OnConnected, OnMessage, OnClose) |

---

## Relationship with Shark-Socket

### Dependency Graph

```
+-----------------------------------------------------------+
|                       shark-mqtt                          |
|                                                           |
|  +-----------------------------------------------------+ |
|  |              shark-mqtt core                         | |
|  |  Broker . TopicTree . QoSEngine . Manager            | |
|  |  Codec . WillHandler . Session . Authenticator       | |
|  +-----------------------------------------------------+ |
|                                                           |
|  +-----------------------------------------------------+ |
|  |           tests/integration/sharksocket/              | |
|  |    shark-socket integration tests (adapter)          | |
|  +-----------------------------------------------------+ |
+-----------------------------------------------------------+
```

### Usage Modes

```
Mode A: Standalone (default)
---------------------------------
  import "github.com/X1aSheng/shark-mqtt/api"

  cfg := config.DefaultConfig()
  cfg.ListenAddr = ":1883"
  broker := api.NewBroker(
      api.WithConfig(cfg),
      api.WithAuth(broker.AllowAllAuth{}),
  )
  broker.Start()


Mode B: Embedding the Broker Core
----------------------------------
  import "github.com/X1aSheng/shark-mqtt/broker"

  b := broker.New(
      broker.WithAuth(myAuth),
      broker.WithSessionStore(store),
  )
  b.Start()
  // manage network layer yourself


Mode C: Blocking Run
---------------------
  import "github.com/X1aSheng/shark-mqtt/api"

  ctx, cancel := context.WithCancel(context.Background())
  defer cancel()
  api.Run(ctx, api.WithConfig(cfg))
```

---

## Design Principles

### P1 -- Network Layer Separated from Business Layer

```
MQTTServer (network layer):
  Responsibilities: TCP Accept, TLS, connection read/write, connection counting
  Dependencies: net, sync, context, config, protocol
  Testability: requires real network (or mock connections)

Broker (business layer):
  Responsibilities: packet routing, session management, QoS state machine, message delivery
  Dependencies: no network dependency (operates connections through Session and clientState)
  Testability: in-memory unit tests, no network needed
```

### P2 -- Connection and Session Separation

```
Connection:
  Lifecycle = TCP connection established to disconnected
  Destroyed on disconnect

Session (broker.Session):
  Lifecycle >= connection lifecycle (when CleanSession=false)
  Can persist across connections
  Holds: subscription list, QoS inflight queue
  State machine: Disconnected -> Connecting -> Connected -> Disconnecting
```

### P3 -- Storage Separated from Runtime

```
All persistence operations abstracted through interfaces:
  SessionStore   -> session persistence
  MessageStore   -> QoS1/2 message persistence
  RetainedStore  -> retained message storage

Default in-memory implementations: ready to use, no external dependencies
Production replacements: Redis / BadgerDB implementations, no business code changes
```

### P4 -- QoS State Machine Encapsulation

```
QoSEngine encapsulates all QoS 1/2 state transitions:
  External calls: TrackQoS1 / TrackQoS2 / AckQoS1 / AckPubRec / AckPubRel / AckPubComp
  Internal management: inflight messages, retry timer, max inflight limit

Business layer does not perceive QoS state transition details
```

### P5 -- Observability First

```
All critical paths instrumented:
  Connection established/disconnected -> Metrics.IncConnections / DecConnections
  Message published                   -> Metrics.IncMessagesPublished
  QoS retry                           -> Metrics.IncRetries
  Authentication failure              -> Metrics.IncAuthFailures

Structured logging: all operations carry clientID, topic, QoS and other fields
```

---

## Layered Architecture

```
+--------------------------------------------------------------------+
|                           API Layer                                 |
|   api/ -- unified entry point, factory methods, type exports        |
+--------------------------------------------------------------------+
|                     Network Layer                                   |
|   broker/                                                           |
|   +--------------------------------------------------------------+  |
|   | MQTTServer                                                   |  |
|   |  +-- TCP Listener                                            |  |
|   |  +-- TLS Support                                             |  |
|   |  +-- Accept Loop                                             |  |
|   |  +-- Connection Handler (ConnectionHandler interface)        |  |
|   +--------------------------------------------------------------+  |
+----------------------------------+---------------------------------+
|           Protocol Layer         |        Infrastructure            |
|   protocol/                     |                                  |
|   +---------------------------+ |   +---------------------------+   |
|   | Codec (3.1.1 + 5.0)      | |   | pkg/logger/ Logger        |   |
|   | Packets (15 types)        | |   | pkg/metrics/ Metrics      |   |
|   | Properties (MQTT 5.0)     | |   | pkg/bufferpool/ Pool      |   |
|   | TopicFilter               | |   | config/ Config, Loader    |   |
|   +---------------------------+ |   | errs/ Sentinel errors     |   |
+----------------------------------+---+---------------------------+---+
|                    Broker Core Layer                                |
|   broker/                                                           |
|   +--------------------------------------------------------------+  |
|   | Broker (core orchestration, implements ConnectionHandler)    |  |
|   | TopicTree (trie subscription tree + wildcards)                |  |
|   | QoSEngine (QoS 1/2 state machine + retry)                    |  |
|   | WillHandler (will message trigger with delay support)         |  |
|   | Session (per-client state machine)                            |  |
|   | Manager (session lifecycle management)                        |  |
|   | Authenticator / Authorizer (auth interfaces + impls)          |  |
|   +--------------------------------------------------------------+  |
+--------------------------------------------------------------------+
|                     Storage Layer                                   |
|   store/                                                            |
|   +------------+------------+--------------+                        |
|   | SessionStore | MessageStore | RetainedStore | (interfaces)      |
|   +------------+------------+--------------+                        |
|   +------------+------------+--------------+                        |
|   | Memory Impl | Redis Impl  | Badger Impl  |                      |
|   +------------+------------+--------------+                        |
+--------------------------------------------------------------------+
|                     Plugin Layer                                    |
|   plugin/                                                           |
|   +--------------------------------------------------------------+  |
|   | Plugin interface: Name() / Hooks() / Execute()               |  |
|   | Manager: Register / Dispatch / RegisteredPlugins              |  |
|   | Hooks: OnAccept, OnConnected, OnMessage, OnClose              |  |
|   +--------------------------------------------------------------+  |
+--------------------------------------------------------------------+
|                     Client Library                                  |
|   client/                                                           |
|   +--------------------------------------------------------------+  |
|   | MQTTClient: Connect / Publish / Subscribe / Unsubscribe /     |  |
|   |             Disconnect, QoS 0/1/2 support                    |  |
|   +--------------------------------------------------------------+  |
+--------------------------------------------------------------------+
```

---

## Directory Structure

```
shark-mqtt/
|
|-- go.mod                            # module github.com/X1aSheng/shark-mqtt
|-- go.sum
|-- README.md
|-- CONTRIBUTING.md
|-- Makefile
|
|-- api/
|   |-- api.go                        # Unified public API, factory methods, option functions
|   |-- api_test.go
|   |-- doc.go                        # Package documentation with usage examples
|
|-- broker/
|   |-- server.go                     # MQTTServer: network layer entry point
|   |-- broker.go                     # Broker: core business orchestration (implements ConnectionHandler)
|   |-- session.go                    # Session: per-client state + Manager: lifecycle
|   |-- session_types.go              # State, CloseReason, Stats type definitions
|   |-- manager.go                    # Manager (session management, also in session.go)
|   |-- topic_tree.go                 # TopicTree: trie subscription tree + wildcards
|   |-- qos_engine.go                 # QoSEngine: QoS 1/2 state machine + retry
|   |-- will_handler.go               # WillHandler: will message registration and trigger
|   |-- auth.go                       # Authenticator, Authorizer, StaticAuth, AllowAllAuth, DenyAllAuth
|   |-- auth_file.go                  # FileAuth: YAML/JSON file-based authentication
|   |-- auth_noop.go                  # NoopAuth: development-only pass-through
|   |-- auth_chain.go                 # ChainAuth: chain multiple authenticators
|   |-- options.go                    # Broker Option functions
|   |-- options_server.go             # MQTTServer ServerOption functions
|   |-- broker_test.go
|   |-- server_test.go
|   |-- session_test.go
|   |-- topic_tree_test.go
|   |-- qos_engine_boundary_test.go
|   |-- qos_engine_test.go
|   |-- will_handler_test.go
|   |-- auth_test.go
|
|-- protocol/
|   |-- codec.go                      # Codec: encode/decode all MQTT packets
|   |-- packets.go                    # All 15 packet type definitions, FixedHeader, TopicFilter
|   |-- constants.go                  # PacketType constants, version constants, reason codes
|   |-- properties.go                 # MQTT 5.0 Properties encoding/decoding
|   |-- connect.go                    # CONNECT packet encode/decode
|   |-- publish.go                    # PUBLISH packet encode/decode
|   |-- subscribe.go                  # SUBSCRIBE/UNSUBSCRIBE encode/decode
|   |-- topic.go                      # MatchTopic utility function
|   |-- errors.go                     # Protocol-level error definitions
|   |-- codec_test.go
|
|-- store/
|   |-- interfaces.go                 # SessionStore, MessageStore, RetainedStore interfaces
|   |-- types.go                      # SessionData, StoredMessage, RetainedMessage, Subscription, InflightMessage
|   |-- errors.go                     # Store-level error definitions
|   |-- memory/
|   |   |-- memory.go                 # All in-memory store implementations
|   |   |-- memory_test.go
|   |   |-- memory_store_test.go
|   |-- redis/
|   |   |-- session_store.go          # Redis session store
|   |   |-- message_store.go          # Redis message store
|   |   |-- retained_store.go         # Redis retained store
|   |-- badger/
|   |   |-- session_store.go          # BadgerDB session store
|   |   |-- message_store.go          # BadgerDB message store
|   |   |-- retained_store.go         # BadgerDB retained store
|   |   |-- badger_store_test.go
|
|-- plugin/
|   |-- manager.go                    # Plugin interface, Manager, Hook constants, Context
|   |-- manager_test.go
|
|-- pkg/
|   |-- logger/
|   |   |-- logger.go                 # Logger interface + slog implementation + Noop
|   |-- metrics/
|   |   |-- metrics.go                # Metrics interface (17 methods) + Noop implementation
|   |-- bufferpool/
|       |-- pool.go                   # Read buffer pool
|       |-- pool_test.go
|
|-- config/
|   |-- config.go                     # Config struct + DefaultConfig() + TLSConfig()
|   |-- loader.go                     # YAML/ENV config loader
|   |-- config_test.go
|
|-- errs/
|   |-- errors.go                     # Global sentinel error definitions
|   |-- errors_test.go
|
|-- client/
|   |-- client.go                     # MQTTClient implementation
|   |-- options.go                    # Client Option functions
|   |-- client_test.go
|
|-- testutils/
|   |-- mock_conn.go                  # MockConn (mock net.Conn)
|   |-- mock_store.go                 # MockSessionStore, MockMessageStore, MockRetainedStore
|   |-- bench.go                      # BenchResult, LatencyRecorder, ThroughputCalculator
|   |-- helpers.go                    # Packet encoding helpers, pipe connections, test utilities
|
|-- cmd/
|   |-- main.go                       # CLI entry point
|
|-- examples/
|   |-- standalone.go                 # Standalone broker example
|   |-- custom_auth.go                # Custom authentication example
|   |-- tls_broker.go                 # TLS broker example
|   |-- sharksocket.go                # Shark-socket integration example
|
|-- test/
|   |-- integration/
|   |   |-- connect_test.go           # CONNECT/CONNACK flow
|   |   |-- pubsub_test.go            # PUBLISH/SUBSCRIBE end-to-end
|   |   |-- qos_test.go               # QoS 0/1/2 complete flow
|   |   |-- persistent_session_test.go # Disconnect/reconnect persistent sessions
|   |   |-- will_test.go              # Will message trigger
|   |   |-- broker_bench_test.go      # Broker benchmarks
|   |   |-- micro_bench_test.go       # Micro-benchmarks
|   |   |-- sharksocket/
|   |       |-- adapter.go            # Shark-socket integration adapter test
|   |-- bench/
```

---

## Core Interface Definitions

### errs Package

```go
// errs/errors.go

package errs

import "errors"

var (
    // Connection and session errors
    ErrSessionClosed       = errors.New("session closed")
    ErrSessionNotFound     = errors.New("session not found")
    ErrWriteQueueFull      = errors.New("write queue full")
    ErrClientIDEmpty       = errors.New("client id is empty")
    ErrClientIDConflict    = errors.New("client id already connected")

    // Protocol errors
    ErrInvalidPacket       = errors.New("invalid packet format")
    ErrUnsupportedVersion  = errors.New("unsupported protocol version")
    ErrIncomplete          = errors.New("incomplete packet")
    ErrPacketTooLarge      = errors.New("packet exceeds maximum size")

    // Authentication errors
    ErrAuthFailed          = errors.New("authentication failed")
    ErrNotAuthorized       = errors.New("not authorized")

    // QoS errors
    ErrInflightFull        = errors.New("inflight queue full")
    ErrDuplicatePacketID   = errors.New("duplicate packet id")
    ErrInvalidQoS          = errors.New("invalid qos level")

    // Storage errors
    ErrStoreUnavailable    = errors.New("store unavailable")
    ErrSessionExpired      = errors.New("session expired")

    // Server errors
    ErrServerClosed        = errors.New("server closed")
    ErrAlreadyStarted      = errors.New("already started")
)
```

---

## Network Layer

### broker/server.go

The `MQTTServer` handles incoming MQTT connections over TCP. It is defined in the `broker` package alongside `Broker`, keeping both network and business layers in the same package while maintaining a clean separation of concerns.

```go
// broker/server.go

package broker

import (
    "context"
    "crypto/tls"
    "fmt"
    "io"
    "log"
    "net"
    "sync"
    "sync/atomic"
    "time"

    "github.com/X1aSheng/shark-mqtt/config"
    "github.com/X1aSheng/shark-mqtt/protocol"
)

// MQTTServer handles incoming MQTT connections over TCP.
type MQTTServer struct {
    cfg        *config.Config
    listener   net.Listener
    handler    ConnectionHandler
    codec      *protocol.Codec
    connCount  atomic.Int64
    tlsConfig  *tls.Config
    ctx        context.Context
    cancel     context.CancelFunc
    wg         sync.WaitGroup
    mu         sync.Mutex
    conns      map[net.Conn]struct{}
}

// ConnectionHandler is called when a new connection is accepted.
type ConnectionHandler interface {
    HandleConnection(ctx context.Context, conn net.Conn, codec *protocol.Codec) error
}

func NewMQTTServer(cfg *config.Config, opts ...ServerOption) *MQTTServer
func (s *MQTTServer) SetHandler(h ConnectionHandler)
func (s *MQTTServer) Start() error
func (s *MQTTServer) Stop()
func (s *MQTTServer) Addr() net.Addr
func (s *MQTTServer) ConnCount() int64
```

Key design points:
- `MQTTServer` only handles TCP accept, TLS wrapping, and connection counting
- `ConnectionHandler` interface bridges network and business layers
- `Broker` implements `ConnectionHandler` and handles all MQTT protocol logic
- TLS can be configured via `config.Config.TLSEnabled` or via `WithTLSConfig` server option
- A custom listener can be injected via `WithListener` for testing

---

## Protocol Layer

### protocol/packets.go

```go
// protocol/packets.go

package protocol

// FixedHeader represents the MQTT fixed header for all packet types.
type FixedHeader struct {
    PacketType      PacketType
    Dup             bool
    QoS             uint8
    Retain          bool
    RemainingLength int
}

// Packet is the base interface for all MQTT packets.
type Packet interface {
    GetFixedHeader() *FixedHeader
}

// 15 concrete packet types:
type ConnectPacket     struct { FixedHeader; ProtocolName string; ProtocolVersion uint8; Flags ConnectFlags; KeepAlive uint16; ClientID string; WillTopic string; WillMessage []byte; Username string; Password []byte; Properties *Properties; WillProperties *Properties }
type ConnAckPacket     struct { FixedHeader; ReasonCode byte; SessionPresent bool; Properties *Properties }
type PublishPacket     struct { FixedHeader; PacketID uint16; Topic string; Payload []byte; Properties *Properties }
type PubAckPacket      struct { FixedHeader; PacketID uint16; ReasonCode byte; Properties *Properties }
type PubRecPacket      struct { FixedHeader; PacketID uint16; ReasonCode byte; Properties *Properties }
type PubRelPacket      struct { FixedHeader; PacketID uint16; ReasonCode byte; Properties *Properties }
type PubCompPacket     struct { FixedHeader; PacketID uint16; ReasonCode byte; Properties *Properties }
type SubscribePacket   struct { FixedHeader; PacketID uint16; Topics []TopicFilter; Properties *Properties }
type SubAckPacket      struct { FixedHeader; PacketID uint16; ReasonCodes []byte; Properties *Properties }
type UnsubscribePacket struct { FixedHeader; PacketID uint16; Topics []string; Properties *Properties }
type UnsubAckPacket    struct { FixedHeader; PacketID uint16; ReasonCodes []byte; Properties *Properties }
type PingReqPacket     struct { FixedHeader }
type PingRespPacket    struct { FixedHeader }
type DisconnectPacket  struct { FixedHeader; ReasonCode byte; Properties *Properties }
type AuthPacket        struct { FixedHeader; ReasonCode byte; Properties *Properties }

// ConnectFlags holds the CONNECT flags byte.
type ConnectFlags struct {
    UsernameFlag  bool
    PasswordFlag  bool
    WillRetain    bool
    WillQoS       uint8
    WillFlag      bool
    WillTopicFlag bool
    CleanSession  bool
    Reserved      bool
}

// TopicFilter represents a subscription topic with QoS.
type TopicFilter struct {
    Topic string
    QoS   uint8
}
```

### protocol/constants.go

```go
// protocol/constants.go

// 15 PacketType constants
const (
    PacketTypeReserved    PacketType = 0
    PacketTypeConnect     PacketType = 1
    PacketTypeConnAck     PacketType = 2
    PacketTypePublish     PacketType = 3
    PacketTypePubAck      PacketType = 4
    PacketTypePubRec      PacketType = 5
    PacketTypePubRel      PacketType = 6
    PacketTypePubComp     PacketType = 7
    PacketTypeSubscribe   PacketType = 8
    PacketTypeSubAck      PacketType = 9
    PacketTypeUnsubscribe PacketType = 10
    PacketTypeUnsubAck    PacketType = 11
    PacketTypePingReq     PacketType = 12
    PacketTypePingResp    PacketType = 13
    PacketTypeDisconnect  PacketType = 14
    PacketTypeAuth        PacketType = 15
)

// MQTT protocol version constants
const (
    Version31   = 3   // MQTT 3.1
    Version311  = 4   // MQTT 3.1.1
    Version50   = 5   // MQTT 5.0
)

const (
    ProtocolNameMQTT   = "MQTT"
    ProtocolNameMQIsdp = "MQIsdp"
)

// ConnAck reason codes, PubAck/SubAck reason codes, SubAck failure codes
```

### protocol/codec.go

```go
// protocol/codec.go

// Codec handles encoding and decoding of MQTT packets.
type Codec struct {
    maxPacketSize int
}

func NewCodec(maxPacketSize int) *Codec
func (c *Codec) Decode(r io.Reader) (Packet, error)
func (c *Codec) Encode(w io.Writer, p Packet) error
```

The Codec:
- `NewCodec(maxPacketSize)` creates a codec with the specified max packet size (0 or negative defaults to 256KB)
- `Decode(r)` reads a packet from any `io.Reader`, automatically detecting packet type
- `Encode(w, p)` writes a packet to any `io.Writer`, dispatching to the correct encoder
- Supports all 15 packet types including MQTT 5.0 AUTH
- Variable-length remaining length encoding/decoding
- MQTT 5.0 properties encoding/decoding via `decodeProperties`/`encodeProperties`

---

## Broker Core Layer

### broker/broker.go

The `Broker` is the core MQTT message broker that orchestrates TopicTree, QoSEngine, WillHandler, and session management. It implements `ConnectionHandler`.

```go
// broker/broker.go

package broker

type Broker struct {
    topics        *TopicTree
    qos           *QoSEngine
    will          *WillHandler
    sessions      *Manager

    sessionStore  store.SessionStore
    messageStore  store.MessageStore
    retainedStore store.RetainedStore

    logger    logger.Logger
    metrics   metrics.Metrics
    pluginMgr *plugin.Manager
    codec     *protocol.Codec

    mu sync.RWMutex
    connections map[string]*clientState  // clientID -> connection state

    ctx    context.Context
    cancel context.CancelFunc
    opts   brokerOptions
}

func New(opts ...Option) *Broker
func (b *Broker) Start() error
func (b *Broker) Stop()
func (b *Broker) HandleConnection(ctx context.Context, conn net.Conn, codec *protocol.Codec) error
```

Connection lifecycle in `HandleConnection`:
1. Dispatch plugin hook `OnAccept`
2. Set CONNECT timeout, decode CONNECT packet
3. Authenticate via `Authenticator.Authenticate(ctx, clientID, username, password)`
4. Create or resume session via `Manager.CreateSession`
5. Register client connection in connections map
6. Register will message if present
7. Dispatch plugin hook `OnConnected`
8. Send CONNACK
9. Enter read loop (`readLoop`)
10. On disconnect, cleanup: remove will, remove session, remove inflight, dispatch `OnClose`

### broker/topic_tree.go

```go
// broker/topic_tree.go

// TopicTree implements a trie-based subscription matching system.
type TopicTree struct {
    root *TopicNode
    mu   sync.RWMutex
}

type TopicNode struct {
    children    map[string]*TopicNode
    subscribers map[string]uint8  // clientID -> qos
}

type Subscriber struct {
    ClientID string
    QoS      uint8
}

func NewTopicTree() *TopicTree
func (tt *TopicTree) Subscribe(topic, clientID string, qos uint8)
func (tt *TopicTree) SubscribeSystem(topic, clientID string, qos uint8)
func (tt *TopicTree) Unsubscribe(topic, clientID string)
func (tt *TopicTree) Match(topic string) []Subscriber
```

Wildcard rules (MQTT specification):
- `+` matches exactly one level: `sensor/+/temp` matches `sensor/room1/temp`
- `#` matches zero or more remaining levels: `sensor/#` matches `sensor/room1/temp`
- System topics (`$SYS/...`) are not matched by `+` or `#` wildcards at the first level

### broker/qos_engine.go

```go
// broker/qos_engine.go

// QoSEngine manages QoS 1/2 message state machines.
type QoSEngine struct {
    inflight     map[string]map[uint16]*InflightMessage  // clientID -> packetID -> msg
    maxInflight  int
    retryInterval time.Duration
    maxRetries    int
    // ... callbacks and lifecycle fields
}

func NewQoSEngine(opts ...QoSOption) *QoSEngine
func (q *QoSEngine) Start()
func (q *QoSEngine) Stop()
func (q *QoSEngine) SetCallbacks(sendPubAck, sendPubRel, sendPubComp, republish) 
func (q *QoSEngine) TrackQoS1(clientID string, packetID uint16, topic string, payload []byte, retain bool)
func (q *QoSEngine) TrackQoS2(clientID string, packetID uint16, topic string, payload []byte, retain bool)
func (q *QoSEngine) AckQoS1(clientID string, packetID uint16)
func (q *QoSEngine) AckPubRec(clientID string, packetID uint16)
func (q *QoSEngine) AckPubRel(clientID string, packetID uint16)
func (q *QoSEngine) AckPubComp(clientID string, packetID uint16)
func (q *QoSEngine) RemoveClient(clientID string)
func (q *QoSEngine) InflightCount(clientID string) int
```

QoS Options:
```go
func WithMaxInflight(n int) QoSOption       // default: 100
func WithRetryInterval(d time.Duration) QoSOption  // default: 10s
func WithMaxRetries(n int) QoSOption        // default: 3
```

### broker/will_handler.go

```go
// broker/will_handler.go

type WillHandler struct { ... }
type WillMessage struct {
    ClientID string
    Topic    string
    Payload  []byte
    QoS      uint8
    Retain   bool
    Delay    time.Duration
}

func NewWillHandler() *WillHandler
func (wh *WillHandler) SetPublishCallback(fn func(topic string, payload []byte, qos uint8, retain bool) error)
func (wh *WillHandler) RegisterWill(clientID, topic string, payload []byte, qos uint8, retain bool, delay time.Duration)
func (wh *WillHandler) TriggerWill(clientID string) error   // triggers on abnormal disconnect
func (wh *WillHandler) CancelWill(clientID string)           // cancels delayed will
func (wh *WillHandler) RemoveWill(clientID string)           // removes without triggering (graceful disconnect)
func (wh *WillHandler) GetWillInfo(clientID string) (*WillInfo, bool)
func (wh *WillHandler) Stop()
```

Will messages support MQTT 5.0 delayed delivery via `Delay` field.

---

## Session Management

Session types and the session manager live in `broker/session.go` and `broker/session_types.go`.

### broker/session_types.go

```go
// broker/session_types.go

// State represents the current state of a session.
type State int

const (
    StateDisconnected  State = iota
    StateConnecting
    StateConnected
    StateDisconnecting
)

// CloseReason represents why a session was closed.
type CloseReason int

const (
    ReasonClientDisconnect       CloseReason = iota
    ReasonServerKick
    ReasonServerShut
    ReasonKeepAliveExpired
    ReasonProtocolError
    ReasonReplacedByNewConnection
)

// Stats holds session statistics.
type Stats struct {
    MessagesReceived uint64
    MessagesSent     uint64
    BytesReceived    uint64
    BytesSent        uint64
    ConnectCount     uint64
    LastConnectedAt  time.Time
    Subscriptions    int
    InflightCount    int
}
```

### broker/session.go

```go
// broker/session.go

// Session represents an active MQTT client session.
type Session struct {
    ClientID       string
    Username       string
    IsClean        bool
    ProtocolVer    uint8
    KeepAlive      uint16
    ConnectedAt    time.Time
    LastActivity   time.Time
    Subscriptions  map[string]uint8   // topic -> qos
    Inflight       map[uint16]*InflightMsg
    // ... internal fields
}

// InflightMsg tracks an in-flight QoS message.
type InflightMsg struct {
    PacketID  uint16
    QoS       uint8
    Topic     string
    Payload   []byte
    Retain    bool
    SentAt    time.Time
    AckType   byte
}

// Session methods
func (s *Session) UpdateActivity()
func (s *Session) IsExpired() bool
func (s *Session) AddSubscription(topic string, qos uint8)
func (s *Session) RemoveSubscription(topic string)
func (s *Session) MatchesSubscription(topic string) (bool, uint8)
func (s *Session) NextPacketID() uint16
func (s *Session) AddInflight(msg *InflightMsg)
func (s *Session) RemoveInflight(packetID uint16)
func (s *Session) GetInflight(packetID uint16) (*InflightMsg, bool)
func (s *Session) Save(ctx context.Context, sessionStore store.SessionStore) error
func (s *Session) State() State
func (s *Session) SetState(state State)
func (s *Session) CloseReason() CloseReason
func (s *Session) SetCloseReason(reason CloseReason)
func (s *Session) IsConnected() bool
func (s *Session) Stats() Stats
func (s *Session) TrackReceived(msgSize int)
func (s *Session) TrackSent(msgSize int)
```

### broker/manager.go (in session.go)

```go
// Manager manages all client sessions.
type Manager struct {
    sessions map[string]*Session
    store    store.SessionStore
    mu       sync.RWMutex
    kickCB   KickCallback
}

type KickCallback func(oldSession *Session)

func NewManager(sessionStore store.SessionStore, opts ...ManagerOpt) *Manager
func (m *Manager) CreateSession(clientID string, connectPkt *protocol.ConnectPacket, isResuming bool) *Session
func (m *Manager) GetSession(clientID string) (*Session, bool)
func (m *Manager) RemoveSession(clientID string)
func (m *Manager) ListSessions() []string
func (m *Manager) SessionExists(clientID string) bool
func (m *Manager) Restore(ctx context.Context, clientID string) (*Session, error)
```

`CreateSession` handles:
- Kicking existing sessions with the same ClientID (sets `ReasonReplacedByNewConnection`)
- Session resumption when `CleanSession=false` and `isResuming=true`
- Fresh session creation with default state

---

## Storage Layer

### store/interfaces.go

```go
// store/interfaces.go

package store

import "context"

// SessionStore handles session persistence.
type SessionStore interface {
    SaveSession(ctx context.Context, clientID string, data *SessionData) error
    GetSession(ctx context.Context, clientID string) (*SessionData, error)
    DeleteSession(ctx context.Context, clientID string) error
    ListSessions(ctx context.Context) ([]string, error)
    IsSessionExists(ctx context.Context, clientID string) (bool, error)
}

// MessageStore handles message persistence.
type MessageStore interface {
    SaveMessage(ctx context.Context, clientID string, msg *StoredMessage) error
    GetMessage(ctx context.Context, clientID, id string) (*StoredMessage, error)
    DeleteMessage(ctx context.Context, clientID, id string) error
    ListMessages(ctx context.Context, clientID string) ([]*StoredMessage, error)
    ClearMessages(ctx context.Context, clientID string) error
}

// RetainedStore handles retained message storage.
type RetainedStore interface {
    SaveRetained(ctx context.Context, topic string, qos uint8, payload []byte) error
    GetRetained(ctx context.Context, topic string) (*RetainedMessage, error)
    DeleteRetained(ctx context.Context, topic string) error
    MatchRetained(ctx context.Context, pattern string) ([]*RetainedMessage, error)
}
```

### store/types.go

```go
// store/types.go

package store

type SessionData struct {
    ClientID      string
    IsClean       bool
    ExpiryTime    time.Time
    Subscriptions []Subscription
    Inflight      map[uint16]*InflightMessage
}

type Subscription struct {
    Topic string
    QoS   uint8
}

type InflightMessage struct {
    PacketID uint16
    QoS      uint8
    Topic    string
    Payload  []byte
    Retain   bool
}

type StoredMessage struct {
    ID        string
    Topic     string
    QoS       uint8
    Payload   []byte
    Retain    bool
    Timestamp time.Time
}

type RetainedMessage struct {
    Topic     string
    QoS       uint8
    Payload   []byte
    Timestamp time.Time
}
```

### Storage Backends

**Memory** (`store/memory/`):
- `NewSessionStore()`, `NewMessageStore()`, `NewRetainedStore()`
- Thread-safe in-memory maps
- No external dependencies

**Redis** (`store/redis/`):
- Uses `github.com/redis/go-redis/v9`
- JSON serialization for session data
- TTL support for session expiry

**BadgerDB** (`store/badger/`):
- Uses `github.com/dgraph-io/badger/v4`
- Key-value storage on disk
- Suitable for single-node persistent deployments

---

## Authentication and Authorization

### broker/auth.go

```go
// broker/auth.go

// Authenticator handles client authentication.
type Authenticator interface {
    Authenticate(ctx context.Context, clientID, username, password string) error
}

// Authorizer handles topic-level authorization.
type Authorizer interface {
    CanPublish(ctx context.Context, clientID, topic string) bool
    CanSubscribe(ctx context.Context, clientID, topic string) bool
}
```

### Built-in Implementations

**AllowAllAuth** -- allows all authentication and authorization (development only):
```go
type AllowAllAuth struct{}

func (AllowAllAuth) Authenticate(ctx context.Context, clientID, username, password string) error { return nil }
func (AllowAllAuth) CanPublish(ctx context.Context, clientID, topic string) bool   { return true }
func (AllowAllAuth) CanSubscribe(ctx context.Context, clientID, topic string) bool { return true }
```

**DenyAllAuth** -- denies all authentication:
```go
type DenyAllAuth struct{}

func (DenyAllAuth) Authenticate(ctx context.Context, clientID, username, password string) error { return ErrAuthFailed }
func (DenyAllAuth) CanPublish(ctx context.Context, clientID, topic string) bool   { return false }
func (DenyAllAuth) CanSubscribe(ctx context.Context, clientID, topic string) bool { return false }
```

**StaticAuth** -- static credentials with ACL support:
```go
type StaticAuth struct { ... }
type ACL struct {
    PublishTopics   []string
    SubscribeTopics []string
}

func NewStaticAuth() *StaticAuth
func (s *StaticAuth) AddCredentials(username, password string)
func (s *StaticAuth) AddACL(username string, acl *ACL)
func (s *StaticAuth) Authenticate(ctx context.Context, clientID, username, password string) error
func (s *StaticAuth) CanPublish(ctx context.Context, clientID, topic string) bool
func (s *StaticAuth) CanSubscribe(ctx context.Context, clientID, topic string) bool
```

**FileAuth** -- YAML/JSON file-based authentication:
```go
type FileAuth struct { ... }
type UserCredential struct {
    Username  string   `json:"username" yaml:"username"`
    Password  string   `json:"password" yaml:"password"`
    ClientIDs []string `json:"client_ids" yaml:"client_ids"`
}

func NewFileAuth(filePath string) (*FileAuth, error)
func (f *FileAuth) LoadFile(filePath string) error
func (f *FileAuth) Authenticate(ctx context.Context, clientID, username, password string) error
```

**ChainAuth** -- chain multiple authenticators:
```go
type ChainAuth struct { ... }

func NewChainAuth(auths ...Authenticator) *ChainAuth
func (c *ChainAuth) AddAuthenticator(auth Authenticator)
func (c *ChainAuth) Authenticate(ctx context.Context, clientID, username, password string) error
```

**NoopAuth** -- development-only pass-through (no authorization):
```go
type NoopAuth struct{}

func (NoopAuth) Authenticate(ctx context.Context, clientID, username, password string) error { return nil }
```

---

## Plugin Layer

### plugin/manager.go

```go
// plugin/manager.go

package plugin

// Hook represents an event hook in the plugin system.
type Hook string

const (
    OnAccept    Hook = "on_accept"
    OnConnected Hook = "on_connected"
    OnMessage   Hook = "on_message"
    OnClose     Hook = "on_close"
)

// Context provides context for hook calls.
type Context struct {
    ClientID   string
    Username   string
    Topic      string
    Payload    []byte
    QoS        uint8
    Retain     bool
    RemoteAddr string
    Err        error
}

// Plugin is the interface for MQTT broker plugins.
type Plugin interface {
    Name() string
    Hooks() []Hook
    Execute(ctx context.Context, hook Hook, data *Context) error
}

// Manager manages plugins and dispatches hook events.
type Manager struct { ... }

func NewManager() *Manager
func (pm *Manager) Register(p Plugin)
func (pm *Manager) Dispatch(ctx context.Context, hook Hook, data *Context) error
func (pm *Manager) RegisteredPlugins() []string
```

### Plugin Example

```go
// A logging plugin example
type LoggingPlugin struct{}

func (p *LoggingPlugin) Name() string { return "logging" }

func (p *LoggingPlugin) Hooks() []plugin.Hook {
    return []plugin.Hook{plugin.OnConnected, plugin.OnClose}
}

func (p *LoggingPlugin) Execute(ctx context.Context, hook plugin.Hook, data *plugin.Context) error {
    switch hook {
    case plugin.OnConnected:
        log.Printf("client connected: %s", data.ClientID)
    case plugin.OnClose:
        log.Printf("client disconnected: %s", data.ClientID)
    }
    return nil
}
```

---

## API Layer

### api/api.go

The `api` package is the unified entry point for Shark-MQTT. It combines `broker.MQTTServer` with `broker.Broker` into a single `api.Broker` type.

```go
// api/api.go

package api

// Broker is the main MQTT broker that combines network server with broker core.
type Broker struct {
    srv    *broker.MQTTServer
    broker *broker.Broker
    cfg    *config.Config
    auth   broker.Authenticator
    authz  broker.Authorizer
}

type Option func(*brokerOpts)

// Option functions
func WithAuth(a broker.Authenticator) Option
func WithAuthorizer(a broker.Authorizer) Option
func WithConfig(cfg *config.Config) Option
func WithSessionStore(s store.SessionStore) Option
func WithMessageStore(s store.MessageStore) Option
func WithRetainedStore(s store.RetainedStore) Option
func WithLogger(l logger.Logger) Option
func WithMetrics(m metrics.Metrics) Option
func WithPluginManager(m *plugin.Manager) Option

// Factory and lifecycle
func NewBroker(opts ...Option) *Broker
func (b *Broker) Start() error
func (b *Broker) Stop()
func (b *Broker) Addr() string
func (b *Broker) ConnCount() int64
func (b *Broker) Broker() *broker.Broker

// Blocking mode
func Run(ctx context.Context, opts ...Option) error
```

### Usage Example

```go
// Standalone broker
cfg := config.DefaultConfig()
cfg.ListenAddr = ":1883"
broker := api.NewBroker(
    api.WithConfig(cfg),
    api.WithAuth(broker.AllowAllAuth{}),
)
if err := broker.Start(); err != nil {
    log.Fatal(err)
}
defer broker.Stop()

// Or blocking mode:
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
api.Run(ctx, api.WithConfig(cfg), api.WithAuth(broker.AllowAllAuth{}))
```

### cmd/main.go

```go
func main() {
    cfg := config.DefaultConfig()
    flag.StringVar(&cfg.ListenAddr, "addr", cfg.ListenAddr, "listen address")
    flag.IntVar(&cfg.MaxConnections, "max-conn", cfg.MaxConnections, "max connections")
    flag.BoolVar(&cfg.TLSEnabled, "tls", cfg.TLSEnabled, "enable TLS")
    flag.StringVar(&cfg.TLSCertFile, "tls-cert", cfg.TLSCertFile, "TLS cert file")
    flag.StringVar(&cfg.TLSKeyFile, "tls-key", cfg.TLSKeyFile, "TLS key file")
    flag.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "log level")
    flag.Parse()

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    b := api.NewBroker(
        api.WithConfig(cfg),
        api.WithAuth(broker.AllowAllAuth{}),
    )
    if err := b.Start(); err != nil {
        log.Fatalf("Failed to start broker: %v", err)
    }
    <-ctx.Done()
    b.Stop()
}
```

---

## Configuration System

### config/config.go

```go
// config/config.go

type Config struct {
    // Server settings
    ListenAddr     string        `yaml:"listen_addr" env:"MQTT_LISTEN_ADDR"`
    TLSEnabled     bool          `yaml:"tls_enabled" env:"MQTT_TLS_ENABLED"`
    TLSCertFile    string        `yaml:"tls_cert_file" env:"MQTT_TLS_CERT_FILE"`
    TLSKeyFile     string        `yaml:"tls_key_file" env:"MQTT_TLS_KEY_FILE"`
    ConnectTimeout time.Duration `yaml:"connect_timeout" env:"MQTT_CONNECT_TIMEOUT"`
    KeepAlive      uint16        `yaml:"keep_alive" env:"MQTT_KEEP_ALIVE"`
    MaxPacketSize  int           `yaml:"max_packet_size" env:"MQTT_MAX_PACKET_SIZE"`
    MaxConnections int           `yaml:"max_connections" env:"MQTT_MAX_CONNECTIONS"`
    WriteQueueSize int           `yaml:"write_queue_size" env:"MQTT_WRITE_QUEUE_SIZE"`

    // QoS settings
    QoSRetryInterval time.Duration `yaml:"qos_retry_interval" env:"MQTT_QOS_RETRY_INTERVAL"`
    QoSMaxRetries    int           `yaml:"qos_max_retries" env:"MQTT_QOS_MAX_RETRIES"`
    QoSMaxInflight   int           `yaml:"qos_max_inflight" env:"MQTT_QOS_MAX_INFLIGHT"`

    // Session settings
    SessionExpiryInterval time.Duration `yaml:"session_expiry_interval" env:"MQTT_SESSION_EXPIRY"`

    // Storage backend: "memory", "redis", "badger"
    StorageBackend string `yaml:"storage_backend" env:"MQTT_STORAGE_BACKEND"`

    // Redis settings
    RedisAddr     string `yaml:"redis_addr" env:"MQTT_REDIS_ADDR"`
    RedisPassword string `yaml:"redis_password" env:"MQTT_REDIS_PASSWORD"`
    RedisDB       int    `yaml:"redis_db" env:"MQTT_REDIS_DB"`

    // BadgerDB settings
    BadgerPath string `yaml:"badger_path" env:"MQTT_BADGER_PATH"`

    // Logging
    LogLevel  string `yaml:"log_level" env:"MQTT_LOG_LEVEL"`
    LogFormat string `yaml:"log_format" env:"MQTT_LOG_FORMAT"`

    // Metrics
    MetricsEnabled  bool   `yaml:"metrics_enabled" env:"MQTT_METRICS_ENABLED"`
    MetricsAddr     string `yaml:"metrics_addr" env:"MQTT_METRICS_ADDR"`
}

func DefaultConfig() *Config
func (c *Config) TLSConfig() (*tls.Config, error)
```

### Default Values

```go
DefaultConfig() returns:
    ListenAddr:            ":1883"
    ConnectTimeout:        10 * time.Second
    KeepAlive:             60
    MaxPacketSize:         256 * 1024  // 256KB
    MaxConnections:        10000
    WriteQueueSize:        256
    QoSRetryInterval:      10 * time.Second
    QoSMaxRetries:         3
    QoSMaxInflight:        100
    SessionExpiryInterval: 24 * time.Hour
    StorageBackend:        "memory"
    LogLevel:              "info"
    LogFormat:             "text"
    MetricsEnabled:        false
    MetricsAddr:           ":9090"
```

### config/loader.go

```go
type Loader struct { ... }

func NewLoader(filePath string) *Loader
func (l *Loader) Load() (*Config, error)
```

Loads configuration from:
1. Environment variables (using `env` struct tags)
2. YAML file (overrides env values)

---

## Observability

### pkg/logger/logger.go

```go
// pkg/logger/logger.go

// Logger interface for structured logging.
type Logger interface {
    Debug(msg string, keyvals ...any)
    Info(msg string, keyvals ...any)
    Warn(msg string, keyvals ...any)
    Error(msg string, keyvals ...any)
}

func New(opts ...Option) Logger
func Default() Logger
func Noop() Logger
func WithLevel(level string) Option
```

Default implementation uses Go's `log/slog`.

### pkg/metrics/metrics.go

```go
// pkg/metrics/metrics.go

// Metrics defines the interface for MQTT metrics collection (17 methods).
type Metrics interface {
    // Connection metrics
    IncConnections()
    DecConnections()
    IncRejections(reason string)
    IncAuthFailures()

    // Message metrics
    IncMessagesPublished(topic string, qos uint8)
    IncMessagesDelivered(clientID string, qos uint8)
    IncMessagesDropped(reason string)

    // QoS metrics
    IncInflight(clientID string)
    DecInflight(clientID string)
    DecInflightBatch(clientID string, count int)
    IncInflightDropped(clientID string)
    IncRetries(clientID string)

    // Session metrics
    SetOnlineSessions(count int)
    SetOfflineSessions(count int)
    SetRetainedMessages(count int)
    SetSubscriptions(count int)

    // System metrics
    IncErrors(component string)
}

func Default() Metrics
```

Default implementation is no-op. Prometheus integration is available via the `prometheus/client_golang` dependency.

---

## Data Flow Design

### Connection Establishment

```
TCP connection arrives
    |
    v
[MQTTServer.acceptLoop]
    | MaxConnections check -> reject if exceeded
    | Spawn goroutine per connection
    v
[MQTTServer -> Broker.HandleConnection]
    | Plugin hook: OnAccept(remoteAddr)
    | Set CONNECT read deadline (10s)
    | codec.Decode(conn) -> ConnectPacket
    | Validate: must be CONNECT
    v
[Broker: Authentication]
    | authenticator.Authenticate(ctx, clientID, username, password)
    | On failure: IncAuthFailures, send ConnAck(BadCredentials), close
    v
[Broker: Session Management]
    | Manager.SessionExists -> check for resume
    | Manager.CreateSession -> kick old session if same ClientID
    | Register connection in Broker.connections map
    | Register will message if WillFlag set
    v
[Broker: Response]
    | Plugin hook: OnConnected(clientID, username)
    | Metrics: IncConnections
    | Send ConnAck(Accepted, sessionPresent)
    v
[Broker: Read Loop]
    | readLoop(clientID, session, codec, conn)
    | For each packet:
    |   Plugin hook: OnMessage(clientID)
    |   Update session activity
    |   Set keep-alive deadline (1.5x KeepAlive)
    |   Dispatch to handler by packet type
    v
[Broker: Cleanup on disconnect]
    | Remove will / trigger will (abnormal disconnect)
    | Manager.RemoveSession
    | QoSEngine.RemoveClient
    | Delete from connections map
    | Plugin hook: OnClose(clientID)
    | Metrics: DecConnections
```

### PUBLISH Message Routing

```
Client sends PUBLISH(topic, payload, QoS=1)
    |
    v
[codec.Decode] -> PublishPacket
    |
    v
[Broker.handlePublish]
    |
    |-- Metrics: IncMessagesPublished(topic, qos)
    |
    |-- Authorization check: authorizer.CanPublish(ctx, clientID, topic)
    |   On failure: IncAuthFailures, send PubAck(NotAuthorized), return
    |
    |-- Retain handling:
    |   Retain=true, payload=empty -> retainedStore.DeleteRetained
    |   Retain=true, payload=present -> retainedStore.SaveRetained
    |
    |-- Route to subscribers: topics.Match(topic) -> []Subscriber
    |   For each subscriber (excluding publisher):
    |     deliverToClient(subscriber, pkt)
    |       Match session subscription -> effective QoS = min(pubQoS, subQoS)
    |       Assign packet ID if QoS > 0
    |       writePacketTo -> encode and send
    |       Metrics: IncMessagesDelivered
    |
    |-- QoS acknowledgment (to publisher):
    |   QoS 1 -> send PubAck, qos.TrackQoS1
    |   QoS 2 -> send PubRec, qos.TrackQoS2
```

### QoS 2 Complete Flow

```
Broker -> PUBLISH(packetID=42, QoS=2) -> Client
         qos.TrackQoS2(clientID, 42, ...)

Client -> PUBREC(packetID=42) -> Broker
         qos.AckPubRec(clientID, 42)
         inflight state -> Acked
         Broker -> PUBREL(packetID=42) -> Client

Client -> PUBCOMP(packetID=42) -> Broker
         qos.AckPubComp(clientID, 42)
         delete(inflight[clientID][42])
         Message delivery complete

Retry path (PUBLISH or PUBREL timeout):
  retryLoop fires periodically
  message exceeds retryInterval -> retries++
  republish callback -> resend PUBLISH
  or sendPubRel callback -> resend PUBREL
  retries >= maxRetries -> remove from inflight

RemoveClient path:
  On disconnect -> qos.RemoveClient(clientID)
  Deletes all inflight for the client
```

---

## Extension Guide

### Implementing a Custom Authenticator

```go
package myauth

type MyAuth struct {
    // your fields
}

func (a *MyAuth) Authenticate(ctx context.Context, clientID, username, password string) error {
    // Check credentials against your backend
    if valid {
        return nil
    }
    return broker.ErrAuthFailed
}
```

Usage:
```go
broker := api.NewBroker(
    api.WithConfig(cfg),
    api.WithAuth(&myauth.MyAuth{}),
)
```

### Implementing a Custom Authorizer

```go
type MyAuthorizer struct{}

func (a *MyAuthorizer) CanPublish(ctx context.Context, clientID, topic string) bool {
    return true // your logic
}

func (a *MyAuthorizer) CanSubscribe(ctx context.Context, clientID, topic string) bool {
    return true // your logic
}
```

Usage:
```go
broker := api.NewBroker(
    api.WithConfig(cfg),
    api.WithAuth(broker.AllowAllAuth{}),
    api.WithAuthorizer(&MyAuthorizer{}),
)
```

### Implementing a Custom Plugin

```go
type ACLPlugin struct {
    rules *ACLRules
}

func (p *ACLPlugin) Name() string { return "acl" }

func (p *ACLPlugin) Hooks() []plugin.Hook {
    return []plugin.Hook{plugin.OnMessage}
}

func (p *ACLPlugin) Execute(ctx context.Context, hook plugin.Hook, data *plugin.Context) error {
    if hook == plugin.OnMessage {
        // inspect or modify data
        if !p.rules.Allow(data.Topic) {
            return fmt.Errorf("topic %s not allowed", data.Topic)
        }
    }
    return nil
}
```

Usage:
```go
pm := plugin.NewManager()
pm.Register(&ACLPlugin{rules: myRules})

broker := api.NewBroker(
    api.WithConfig(cfg),
    api.WithAuth(broker.AllowAllAuth{}),
    api.WithPluginManager(pm),
)
```

### Implementing a Custom Store Backend

```go
// store/redis/session_store.go

package redis

type RedisSessionStore struct {
    client *redis.Client
    ttl    time.Duration
}

// Compile-time assertion
var _ store.SessionStore = (*RedisSessionStore)(nil)

func (s *RedisSessionStore) SaveSession(ctx context.Context, clientID string, data *store.SessionData) error {
    // Serialize and save to Redis
}

func (s *RedisSessionStore) GetSession(ctx context.Context, clientID string) (*store.SessionData, error) {
    // Load and deserialize from Redis
}

func (s *RedisSessionStore) DeleteSession(ctx context.Context, clientID string) error {
    // Delete from Redis
}

func (s *RedisSessionStore) ListSessions(ctx context.Context) ([]string, error) {
    // List all session keys
}

func (s *RedisSessionStore) IsSessionExists(ctx context.Context, clientID string) (bool, error) {
    // Check existence
}
```

---

## Testing Strategy

### Layered Testing

```
Unit tests (no network):
  broker/broker_test.go          -> test routing logic with mock connections
  broker/topic_tree_test.go      -> test wildcard matching all edge cases
  broker/qos_engine_test.go      -> test state machine transitions and retry
  broker/qos_engine_boundary_test.go -> boundary condition tests
  broker/session_test.go         -> test session state machine
  broker/will_handler_test.go    -> test will registration, trigger, cancel
  broker/auth_test.go            -> test all authenticator implementations
  broker/server_test.go          -> test server with mock listener
  protocol/codec_test.go         -> test encode/decode all packet types
  store/memory/memory_test.go    -> test in-memory store implementations
  config/config_test.go          -> test config loading

Integration tests (real connections):
  tests/integration/connect_test.go            -> CONNECT/CONNACK flow
  tests/integration/pubsub_test.go             -> PUBLISH/SUBSCRIBE end-to-end
  tests/integration/qos_test.go                -> QoS 0/1/2 complete flow
  tests/integration/persistent_session_test.go -> disconnect/reconnect persistent sessions
  tests/integration/will_test.go               -> will message trigger

Benchmark tests:
  tests/integration/broker_bench_test.go       -> broker-level benchmarks
  tests/integration/micro_bench_test.go        -> micro-benchmarks
```

### Test Utilities (testutils/)

```
testutils/
|-- mock_conn.go      -> MockConn implements net.Conn for testing
|-- mock_store.go     -> MockSessionStore, MockMessageStore, MockRetainedStore
|-- bench.go          -> BenchResult, LatencyRecorder, ThroughputCalculator
|-- helpers.go        -> MustEncodeConnect, MustEncodePublish, NewPipe, WaitForChannel, etc.
```

### Compile-time Assertion Checklist

```go
// In each implementation file, ensure interface compliance:

// testutils/mock_store.go
var _ store.SessionStore  = (*MockSessionStore)(nil)
var _ store.MessageStore  = (*MockMessageStore)(nil)
var _ store.RetainedStore = (*MockRetainedStore)(nil)

// testutils/mock_conn.go
var _ net.Conn = (*MockConn)(nil)

// broker/auth.go
// AllowAllAuth, DenyAllAuth, StaticAuth implicitly satisfy Authenticator + Authorizer

// broker/auth_file.go
// FileAuth satisfies Authenticator

// broker/auth_chain.go
// ChainAuth satisfies Authenticator
```

### Test Commands

```bash
# All tests (with race detection)
go test -race ./...

# Unit tests only
go test -race ./broker/... ./protocol/... ./store/... ./config/... ./pkg/...

# Integration tests
go test -race ./tests/integration/...

# Benchmarks
go test -bench=. -benchmem -benchtime=10s ./tests/integration/...

# Coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

---

## Architecture Decision Records

### ADR-001: Independent Repository

**Decision**: shark-mqtt as an independent Go module (`github.com/X1aSheng/shark-mqtt`)

**Reason**:
- MQTT code volume is substantial and self-contained
- Optional external dependencies (Redis/BadgerDB) should not pollute other frameworks
- Supports independent versioning, deployment, and scaling
- Integration with shark-socket via `tests/integration/sharksocket/` adapter

---

### ADR-002: Network Layer Separated from Business Layer (MQTTServer vs Broker)

**Decision**: `broker.MQTTServer` handles networking only, `broker.Broker` handles business logic only

**Reason**:
- Broker can be unit tested without network
- Network layer can be independently replaced (WebSocket over MQTT, QUIC over MQTT, etc.)
- Clear responsibilities, each type has only one reason to change
- Both live in the `broker` package but maintain distinct responsibilities

---

### ADR-003: Session State Machine (broker.Session)

**Decision**: `broker.Session` with explicit state machine (`StateDisconnected`, `StateConnecting`, `StateConnected`, `StateDisconnecting`) rather than implicit online/offline management

**Reason**:
- Clear visibility into session lifecycle transitions
- `CloseReason` enum captures why a session ended (client disconnect, server kick, keepalive expired, protocol error, replaced by new connection)
- `Manager` handles all session CRUD with proper kick-callback support
- Session persistence via `Save()` method to `store.SessionStore`

---

### ADR-004: Single broker Package for Core Components

**Decision**: MQTTServer, Broker, Session, Manager, TopicTree, QoSEngine, WillHandler, Authenticator, and Authorizer all live in the `broker` package

**Reason**:
- These components are tightly coupled and form a cohesive unit
- Avoids circular dependency issues between separate packages
- Simplifies internal communication (all types are visible within the package)
- Public interfaces are still exposed through `api/` for external consumers
- The `api/` package provides the clean public API boundary

---

### ADR-005: TopicTree Preserves System Topic Protection

**Decision**: Topics starting with `$` are not matched by `+` and `#` wildcards (conforming to MQTT specification section 4.7.2)

**Reason**:
- MQTT specification explicitly requires: `$SYS/#` is not matched by `#` subscription
- Prevents regular subscribers from accidentally receiving broker internal system messages
- `SubscribeSystem` method exists for explicit system topic subscription

---

### ADR-006: Functional Options Pattern

**Decision**: Use functional options (`type Option func(*options)`) throughout the codebase

**Reason**:
- Consistent API pattern across all components (api, broker, client, QoSEngine, server)
- Extensible without breaking changes
- Clear intent through naming (e.g., `WithAuth`, `WithConfig`, `WithSessionStore`)
- Defaults are applied first, then overridden by options
