# Shark-MQTT 独立架构设计文档 v1.0

> 版本: 1.0.0 | 最后更新: 2026-04-08 | Go 版本: 1.26.1

---

## 目录

1. [项目概述](#项目概述)
2. [与 Shark-Socket 的关系](#与-shark-socket-的关系)
3. [设计原则](#设计原则)
4. [分层架构](#分层架构)
5. [目录结构](#目录结构)
6. [核心接口定义](#核心接口定义)
7. [网络层](#网络层)
8. [协议层](#协议层)
9. [Broker 核心层](#broker-核心层)
10. [存储层](#存储层)
11. [认证层](#认证层)
12. [插件层](#插件层)
13. [API 层](#api-层)
14. [配置系统](#配置系统)
15. [可观测性](#可观测性)
16. [数据流设计](#数据流设计)
17. [扩展指南](#扩展指南)
18. [测试策略](#测试策略)
19. [设计决策记录](#设计决策记录)

---

## 项目概述

Shark-MQTT 是从 Shark-Socket 独立出来的高性能 MQTT Broker 实现，支持 **MQTT 3.1.1** 和 **MQTT 5.0** 协议，提供完整的 QoS 0/1/2、主题通配符、持久会话、遗嘱消息和保留消息支持。

### 独立原因

```
MQTT 与其他协议的本质差异，决定了它需要独立维护：

协议复杂度：
  其他协议状态机  → 2 个状态（CONNECTED / CLOSED）
  MQTT 状态机    → 5+ 个状态（含 OFFLINE 持久会话）

资源模型：
  其他协议  → 连接 ≡ 资源，断开即释放
  MQTT     → 会话 ≠ 连接，离线会话持续占用内存

子系统规模：
  其他单协议  → 500 ~ 1500 行
  MQTT 完整  → 8000 ~ 20000 行

外部依赖：
  其他协议  → 标准库
  MQTT     → 可选 Redis / BadgerDB（持久化）

团队边界：
  独立仓库 → 独立发版、独立扩缩容、独立演进
```

### 核心特性

| 特性 | 说明 |
|------|------|
| 协议版本 | MQTT 3.1.1 / 5.0 双版本支持 |
| QoS 级别 | QoS 0 / 1 / 2 完整支持 |
| 持久会话 | CleanSession=false 跨连接会话保持 |
| 主题通配符 | `+` 单层、`#` 多层通配符匹配 |
| 遗嘱消息 | Will Message 触发机制 |
| 保留消息 | Retained Message 存储与投递 |
| 离线消息 | QoS1/2 离线消息队列 |
| 可插拔存储 | 内存 / Redis / BadgerDB |
| 可插拔认证 | 接口化认证，支持自定义实现 |
| 与框架集成 | 可选集成 Shark-Socket 插件和会话管理 |

---

## 与 Shark-Socket 的关系

### 依赖关系图

```
┌─────────────────────────────────────────────────────────┐
│                    shark-mqtt                           │
│                                                         │
│  ┌─────────────────────────────────────────────────┐   │
│  │              shark-mqtt 核心                     │   │
│  │  Broker · TopicTree · QoSEngine · SessionMgr    │   │
│  │  Codec · WillHandler · RetainedStore            │   │
│  └─────────────────────────────────────────────────┘   │
│                         │                               │
│              可选集成接口层                              │
│                         │                               │
│  ┌──────────────────────▼──────────────────────────┐   │
│  │           shark-socket-api（共享契约）            │   │
│  │    NetworkSession · ProtocolServer · Plugin      │   │
│  └──────────────────────┬──────────────────────────┘   │
└─────────────────────────┼───────────────────────────────┘
                          │ 可选依赖
┌─────────────────────────▼───────────────────────────────┐
│                   shark-socket                          │
│         Gateway · SessionManager · PluginRegistry       │
└─────────────────────────────────────────────────────────┘
```

### 三种使用模式

```
模式 A：独立运行（不依赖 shark-socket）
─────────────────────────────────────────
  import "github.com/yourorg/shark-mqtt/api"

  broker := api.NewBroker(
      api.WithAddr(":1883"),
      api.WithAuth(myAuth),
  )
  broker.Start()


模式 B：嵌入 shark-socket Gateway
───────────────────────────────────
  import (
      "github.com/yourorg/shark-socket/api" as ss
      "github.com/yourorg/shark-mqtt/integration/sharksocket"
  )

  gw := ss.NewGateway().
      WithTCP[MyMsg](":9000", proto, proc).
      AddServer(sharksocket.NewMQTTAdapter(":1883", mqttOpts...)).
      Build()
  gw.Start()


模式 C：仅使用 Broker 核心（嵌入其他框架）
───────────────────────────────────────────
  import "github.com/yourorg/shark-mqtt/broker"

  b := broker.New(broker.Config{...})
  // 自行管理网络层
```

### 版本契约

```
shark-socket-api  → 共享接口包，语义化版本严格遵守
shark-mqtt        → 独立版本，major 版本跟随 shark-socket-api

兼容性矩阵：
  shark-mqtt v1.x → shark-socket-api v1.x → shark-socket v1.x
  shark-mqtt v2.x → shark-socket-api v2.x → shark-socket v2.x
```

---

## 设计原则

### P1 — 网络层与业务层分离

```
MQTTServer（网络层）：
  职责：TCP Accept、TLS、连接读写、框架层对接
  依赖：net、sync、context
  可测试性：需要真实网络

Broker（业务层）：
  职责：报文路由、会话管理、QoS 状态机、消息投递
  依赖：无网络依赖（通过 Session 接口操作连接）
  可测试性：纯内存单元测试，无需网络
```

### P2 — 连接与会话分离

```
连接（Connection）：
  生命周期 = TCP 连接建立到断开
  断开即销毁

会话（Session）：
  生命周期 >= 连接生命周期（CleanSession=false 时）
  可跨连接持久存在
  持有：订阅列表、QoS 待确认队列

关键操作：
  Attach(conn)  → 会话绑定新连接（重连）
  Detach()      → 会话解绑连接（断开，保留状态）
  Close()       → 销毁会话对象（CleanSession=true）
```

### P3 — 存储与运行时分离

```
所有持久化操作通过接口抽象：
  SessionStore   → 会话持久化
  MessageStore   → QoS1/2 消息持久化
  RetainedStore  → 保留消息存储

默认内存实现：开箱即用，无外部依赖
生产替换：Redis / BadgerDB 实现，不修改业务代码
```

### P4 — QoS 状态机封装

```
QoSEngine 封装所有 QoS 1/2 的状态转换：
  外部只调用：Enqueue / AckQoS1 / AckPubRec / AckPubComp
  内部管理：在途消息、重试定时器、最大在途数限制

业务层不感知 QoS 状态转换细节
```

### P5 — 可观测性优先

```
所有关键路径埋点：
  连接建立/断开 → Metrics.IncConnections / DecConnections
  消息发布      → Metrics.IncMessages
  QoS 重试      → Metrics.IncRetries
  认证失败      → Metrics.IncAuthFailures

结构化日志：所有操作携带 clientID、topic、QoS 等字段
```

---

## 分层架构

```
┌────────────────────────────────────────────────────────────────────┐
│                         API Layer                                  │
│              api/ —— 统一入口、工厂方法、类型导出                   │
├────────────────────────────────────────────────────────────────────┤
│                      Integration Layer                             │
│  integration/sharksocket/ —— shark-socket Gateway 适配器           │
├──────────────────────────────────────┬─────────────────────────────┤
│           Network Layer              │      Infrastructure         │
│   server/                            │                             │
│   ┌──────────────────────────────┐   │   ┌─────────────────────┐  │
│   │ MQTTServer                   │   │   │ Logger              │  │
│   │  ├── TCP Listener            │   │   │ Metrics             │  │
│   │  ├── TLS Support             │   │   │ Config              │  │
│   │  ├── Accept Loop             │   │   │ BufferPool          │  │
│   │  └── Connection Handler      │   │   └─────────────────────┘  │
│   └──────────────────────────────┘   │                             │
├──────────────────────────────────────┤                             │
│           Protocol Layer             │                             │
│   protocol/                          │                             │
│   ┌──────────────────────────────┐   │                             │
│   │ Codec（3.1.1 + 5.0）         │   │                             │
│   │ Packets（所有报文类型）       │   │                             │
│   │ Handler（报文分发）          │   │                             │
│   └──────────────────────────────┘   │                             │
├──────────────────────────────────────┤                             │
│           Broker Core Layer          │                             │
│   broker/                            │                             │
│   ┌──────────────────────────────┐   │                             │
│   │ Broker（核心编排）            │   │                             │
│   │ TopicTree（订阅树）           │   │                             │
│   │ QoSEngine（状态机）           │   │                             │
│   │ WillHandler（遗嘱）          │   │                             │
│   └──────────────────────────────┘   │                             │
├──────────────────────────────────────┤                             │
│           Session Layer              │                             │
│   session/                           │                             │
│   ┌──────────────────────────────┐   │                             │
│   │ MQTTSession                  │   │                             │
│   │ SessionRegistry（持久会话）   │   │                             │
│   │ Manager（连接维度管理）       │   │                             │
│   └──────────────────────────────┘   │                             │
├──────────────────────────────────────┤                             │
│           Storage Layer              │                             │
│   store/                             │                             │
│   ┌──────────┬──────────┬─────────┐  │                             │
│   │ Session  │ Message  │Retained │  │                             │
│   │ Store    │ Store    │ Store   │  │                             │
│   │ (接口)   │ (接口)   │ (接口)  │  │                             │
│   └──────────┴──────────┴─────────┘  │                             │
│   ┌──────────┬──────────┬─────────┐  │                             │
│   │ Memory   │  Redis   │ Badger  │  │                             │
│   │  Impl    │  Impl    │  Impl   │  │                             │
│   └──────────┴──────────┴─────────┘  │                             │
├──────────────────────────────────────┤                             │
│           Auth Layer                 │                             │
│   auth/                              │                             │
│   ┌──────────────────────────────┐   │                             │
│   │ Authenticator（接口）        │   │                             │
│   │ NoopAuth / FileAuth / ...    │   │                             │
│   └──────────────────────────────┘   │                             │
└──────────────────────────────────────┴─────────────────────────────┘
```

---

## 目录结构

```
shark-mqtt/
│
├── go.mod                         # module github.com/yourorg/shark-mqtt
├── go.sum
├── README.md
│
├── api/
│   └── api.go                     # 统一公共 API、工厂方法、类型导出
│
├── integration/
│   └── sharksocket/
│       ├── adapter.go             # 实现 ProtocolServer 接口（供 Gateway 使用）
│       ├── session_bridge.go      # MQTTSession → NetworkSession 桥接
│       └── adapter_test.go
│
├── server/
│   ├── server.go                  # MQTTServer：网络层入口
│   ├── tls.go                     # TLS 支持
│   ├── options.go                 # 服务器选项
│   ├── conn_handler.go            # 单连接处理逻辑
│   └── server_test.go
│
├── protocol/
│   ├── codec.go                   # 编解码器主入口（3.1.1 + 5.0）
│   ├── codec_v311.go              # MQTT 3.1.1 编解码实现
│   ├── codec_v50.go               # MQTT 5.0 编解码实现
│   ├── packets.go                 # 所有报文类型定义
│   ├── packet_types.go            # 报文类型常量
│   ├── properties.go              # MQTT 5.0 Properties 定义
│   ├── codec_test.go
│   └── packets_test.go
│
├── broker/
│   ├── broker.go                  # Broker：核心业务编排
│   ├── topic_tree.go              # TopicTree：Trie 订阅树 + 通配符
│   ├── qos_engine.go              # QoSEngine：QoS 1/2 状态机 + 重试
│   ├── will_handler.go            # WillHandler：遗嘱消息触发
│   ├── options.go                 # Broker 选项
│   ├── broker_test.go
│   ├── topic_tree_test.go
│   └── qos_engine_test.go
│
├── session/
│   ├── types.go                   # 类型定义：State、CloseReason、Stats
│   ├── session.go                 # MQTTSession：会话核心实现
│   ├── registry.go                # SessionRegistry：持久会话管理
│   ├── manager.go                 # Manager：连接维度管理（可选）
│   ├── session_test.go
│   └── registry_test.go
│
├── store/
│   ├── interfaces.go              # 存储接口定义
│   ├── types.go                   # 存储相关类型
│   ├── memory/
│   │   ├── session_store.go       # 内存会话存储
│   │   ├── message_store.go       # 内存消息存储
│   │   ├── retained_store.go      # 内存保留消息存储
│   │   └── memory_test.go
│   ├── redis/
│   │   ├── session_store.go       # Redis 会话存储
│   │   ├── message_store.go       # Redis 消息存储
│   │   ├── retained_store.go      # Redis 保留消息存储
│   │   └── redis_test.go
│   └── badger/
│       ├── session_store.go       # BadgerDB 会话存储
│       ├── message_store.go       # BadgerDB 消息存储
│       ├── retained_store.go      # BadgerDB 保留消息存储
│       └── badger_test.go
│
├── auth/
│   ├── auth.go                    # Authenticator 接口 + AuthRequest
│   ├── noop.go                    # NoopAuthenticator：全部放行
│   ├── file.go                    # FileAuthenticator：文件配置认证
│   ├── chain.go                   # ChainAuthenticator：链式认证
│   └── auth_test.go
│
├── plugin/
│   ├── plugin.go                  # MQTTPlugin 接口（MQTT 特有钩子）
│   ├── base.go                    # BaseMQTTPlugin：默认实现
│   ├── registry.go                # MQTTPluginRegistry
│   ├── acl.go                     # 内置：ACL 访问控制
│   ├── ratelimit.go               # 内置：发布速率限制
│   ├── audit.go                   # 内置：审计日志
│   └── plugin_test.go
│
├── infra/
│   ├── logger/
│   │   ├── logger.go              # Logger 接口
│   │   └── slog.go                # slog 默认实现
│   ├── metrics/
│   │   ├── metrics.go             # Metrics 接口
│   │   ├── noop.go                # 空实现
│   │   └── prometheus.go          # Prometheus 实现
│   └── bufferpool/
│       └── pool.go                # 读缓冲区池
│
├── config/
│   ├── config.go                  # Config 结构体定义
│   ├── loader.go                  # YAML/TOML/ENV 加载
│   └── config_test.go
│
├── errs/
│   └── errors.go                  # 全局错误类型定义
│
├── client/
│   ├── client.go                  # MQTTClient 实现
│   ├── options.go                 # 客户端选项
│   └── client_test.go
│
├── testutils/
│   ├── mock_conn.go               # mock net.Conn
│   ├── mock_session.go            # mock MQTTSession
│   ├── mock_store.go              # mock Store 实现
│   ├── bench.go                   # 基准测试工具
│   └── fixtures/
│       ├── certs/                 # 测试证书
│       └── packets/               # 测试报文二进制文件
│
├── examples/
│   ├── standalone/                # 独立运行示例
│   ├── with_sharksocket/          # 集成 shark-socket 示例
│   ├── custom_auth/               # 自定义认证示例
│   ├── redis_store/               # Redis 存储示例
│   └── tls_broker/                # TLS 示例
│
└── test/
    ├── integration/               # 端到端集成测试
    │   ├── connect_test.go
    │   ├── pubsub_test.go
    │   ├── qos_test.go
    │   ├── persistent_session_test.go
    │   └── will_test.go
    └── bench/
        ├── throughput_test.go
        └── latency_test.go
```

---

## 核心接口定义

### errs 包

```go
// errs/errors.go

package errs

import "errors"

var (
    // 连接与会话错误
    ErrSessionClosed       = errors.New("session closed")
    ErrSessionNotFound     = errors.New("session not found")
    ErrWriteQueueFull      = errors.New("write queue full")
    ErrClientIDEmpty       = errors.New("client id is empty")
    ErrClientIDConflict    = errors.New("client id already connected")

    // 协议错误
    ErrInvalidPacket       = errors.New("invalid packet format")
    ErrUnsupportedVersion  = errors.New("unsupported protocol version")
    ErrIncomplete          = errors.New("incomplete packet")
    ErrPacketTooLarge      = errors.New("packet exceeds maximum size")

    // 认证错误
    ErrAuthFailed          = errors.New("authentication failed")
    ErrNotAuthorized       = errors.New("not authorized")

    // QoS 错误
    ErrInflightFull        = errors.New("inflight queue full")
    ErrDuplicatePacketID   = errors.New("duplicate packet id")
    ErrInvalidQoS          = errors.New("invalid qos level")

    // 存储错误
    ErrStoreUnavailable    = errors.New("store unavailable")
    ErrSessionExpired      = errors.New("session expired")

    // 服务错误
    ErrServerClosed        = errors.New("server closed")
    ErrAlreadyStarted      = errors.New("already started")
)
```

---

## 网络层

### server.go

```go
// server/server.go

package server

import (
    "context"
    "crypto/tls"
    "fmt"
    "net"
    "sync"
    "sync/atomic"
    "time"

    "github.com/yourorg/shark-mqtt/broker"
    "github.com/yourorg/shark-mqtt/errs"
    "github.com/yourorg/shark-mqtt/infra/logger"
    "github.com/yourorg/shark-mqtt/infra/metrics"
    "github.com/yourorg/shark-mqtt/plugin"
    "github.com/yourorg/shark-mqtt/protocol"
    "github.com/yourorg/shark-mqtt/session"
)

// MQTTServer 网络层入口
//
// 职责边界：
//   MQTTServer → TCP Accept、TLS、连接生命周期管理
//   Broker     → MQTT 协议业务逻辑（报文路由、QoS、会话管理）
//
// 两者分离使 Broker 可以不依赖网络，支持纯单元测试
type MQTTServer struct {
    opts     Options
    broker   *broker.Broker
    codec    *protocol.Codec
    plugins  *plugin.Registry
    logger   logger.Logger
    metrics  metrics.Metrics

    listener    net.Listener
    connCount   atomic.Int64
    stopCh      chan struct{}
    wg          sync.WaitGroup
    mu          sync.Mutex
    started     bool
}

func New(opts ...Option) *MQTTServer {
    o := defaultOptions()
    for _, opt := range opts {
        opt(&o)
    }

    b := broker.New(
        broker.WithSessionStore(o.sessionStore),
        broker.WithMessageStore(o.messageStore),
        broker.WithRetainedStore(o.retainedStore),
        broker.WithAuthenticator(o.authenticator),
        broker.WithLogger(o.logger),
        broker.WithMetrics(o.metrics),
        broker.WithQoSOptions(o.qosOpts...),
    )

    return &MQTTServer{
        opts:    o,
        broker:  b,
        codec:   protocol.NewCodec(),
        plugins: o.pluginRegistry,
        logger:  o.logger,
        metrics: o.metrics,
        stopCh:  make(chan struct{}),
    }
}

// Start 启动服务器
func (s *MQTTServer) Start() error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.started {
        return errs.ErrAlreadyStarted
    }

    ln, err := s.createListener()
    if err != nil {
        return fmt.Errorf("mqtt server listen %s: %w", s.opts.Addr, err)
    }

    s.listener = ln
    s.started = true

    // 启动 Broker 子系统（QoS 重试循环等）
    s.broker.Start()

    s.wg.Add(1)
    go s.acceptLoop()

    s.logger.Info("shark-mqtt broker started",
        "addr", s.opts.Addr,
        "tls", s.opts.TLSConfig != nil,
    )
    return nil
}

// Stop 优雅停止
func (s *MQTTServer) Stop(ctx context.Context) error {
    s.mu.Lock()
    if !s.started {
        s.mu.Unlock()
        return nil
    }
    s.started = false
    close(s.stopCh)
    _ = s.listener.Close()
    s.mu.Unlock()

    // 等待 acceptLoop 退出
    done := make(chan struct{})
    go func() {
        s.wg.Wait()
        close(done)
    }()

    select {
    case <-done:
    case <-ctx.Done():
        return fmt.Errorf("mqtt server stop timeout: %w", ctx.Err())
    }

    // 停止 Broker 子系统
    s.broker.Stop()

    s.logger.Info("shark-mqtt broker stopped")
    return nil
}

func (s *MQTTServer) ActiveConnections() int64 {
    return s.connCount.Load()
}

func (s *MQTTServer) createListener() (net.Listener, error) {
    if s.opts.TLSConfig != nil {
        return tls.Listen("tcp", s.opts.Addr, s.opts.TLSConfig)
    }
    return net.Listen("tcp", s.opts.Addr)
}

func (s *MQTTServer) acceptLoop() {
    defer s.wg.Done()

    for {
        conn, err := s.listener.Accept()
        if err != nil {
            select {
            case <-s.stopCh:
                return
            default:
                s.logger.Warn("mqtt accept error", "err", err)
                continue
            }
        }

        // 插件 OnAccept 检查（连接建立前，零资源分配拒绝路径）
        if !s.plugins.OnAccept(conn.RemoteAddr().String()) {
            _ = conn.Close()
            s.metrics.IncRejections("plugin")
            continue
        }

        s.connCount.Add(1)
        s.wg.Add(1)
        go func() {
            defer s.wg.Done()
            defer s.connCount.Add(-1)
            s.handleConn(conn)
        }()
    }
}
```

### conn_handler.go

```go
// server/conn_handler.go

package server

import (
    "net"
    "time"

    "github.com/yourorg/shark-mqtt/errs"
    "github.com/yourorg/shark-mqtt/protocol"
    "github.com/yourorg/shark-mqtt/session"
)

// handleConn 处理单个连接的完整生命周期
//
// 生命周期：
//   TCP 连接 → CONNECT 解析 → 认证 → 会话建立 → 消息循环 → 断开处理
func (s *MQTTServer) handleConn(conn net.Conn) {
    defer conn.Close()

    // 设置 CONNECT 报文读取超时
    if s.opts.ConnectTimeout > 0 {
        _ = conn.SetReadDeadline(time.Now().Add(s.opts.ConnectTimeout))
    }

    // 第一个报文必须是 CONNECT
    connectPkt, err := s.codec.DecodeConnect(conn)
    if err != nil {
        s.logger.Warn("mqtt decode connect failed",
            "remote", conn.RemoteAddr(),
            "err", err,
        )
        s.metrics.IncErrors("decode_connect")
        return
    }

    // 清除 CONNECT 超时（后续用 KeepAlive 控制）
    _ = conn.SetReadDeadline(time.Time{})

    // Broker 处理 CONNECT（认证 + 会话建立）
    sess, result, err := s.broker.HandleConnect(conn, connectPkt)
    if err != nil {
        returnCode := protocol.ConnectReturnCode(err)
        _ = s.codec.SendConnAck(conn, returnCode, false)
        s.logger.Warn("mqtt connect rejected",
            "remote", conn.RemoteAddr(),
            "clientID", connectPkt.ClientID,
            "err", err,
        )
        s.metrics.IncAuthFailures()
        return
    }

    // 发送 CONNACK
    if err := s.codec.SendConnAck(conn, protocol.ConnAckAccepted, result.SessionPresent); err != nil {
        s.logger.Warn("mqtt send connack failed", "clientID", sess.ClientID(), "err", err)
        return
    }

    // 插件 OnConnected
    s.plugins.OnConnected(sess)
    s.metrics.IncConnections()

    s.logger.Info("mqtt client connected",
        "clientID", sess.ClientID(),
        "sessionPresent", result.SessionPresent,
        "cleanSession", connectPkt.CleanSession,
        "keepAlive", connectPkt.KeepAlive,
        "remote", conn.RemoteAddr(),
    )

    // 进入消息循环
    reason := s.messageLoop(conn, sess)

    // 断开处理
    s.plugins.OnClose(sess, reason)
    s.broker.HandleDisconnect(sess, reason)
    s.metrics.DecConnections()

    s.logger.Info("mqtt client disconnected",
        "clientID", sess.ClientID(),
        "reason", reason,
    )
}

// messageLoop 连接消息读取主循环
// 返回断开原因
func (s *MQTTServer) messageLoop(conn net.Conn, sess *session.MQTTSession) session.CloseReason {
    keepAlive := time.Duration(sess.KeepAlive()) * time.Second

    for {
        // 设置 KeepAlive 超时（1.5 倍容忍抖动）
        if keepAlive > 0 {
            _ = conn.SetReadDeadline(time.Now().Add(keepAlive * 3 / 2))
        }

        pkt, err := s.codec.Decode(conn)
        if err != nil {
            if isTimeoutError(err) {
                return session.CloseReasonTimeout
            }
            select {
            case <-s.stopCh:
                return session.CloseReasonServerShut
            default:
                return session.CloseReasonError
            }
        }

        sess.UpdateLastActive()

        // 插件 OnMessage
        if raw, encErr := s.codec.Encode(pkt); encErr == nil {
            if _, cont := s.plugins.OnMessage(sess, raw); !cont {
                continue
            }
        }

        // 分发到 Broker
        if disconnect := s.broker.Dispatch(sess, pkt); disconnect {
            return session.CloseReasonNormal
        }
    }
}

func isTimeoutError(err error) bool {
    netErr, ok := err.(net.Error)
    return ok && netErr.Timeout()
}
```

### server/options.go

```go
// server/options.go

package server

import (
    "crypto/tls"
    "time"

    "github.com/yourorg/shark-mqtt/auth"
    "github.com/yourorg/shark-mqtt/infra/logger"
    "github.com/yourorg/shark-mqtt/infra/metrics"
    "github.com/yourorg/shark-mqtt/plugin"
    "github.com/yourorg/shark-mqtt/store"
)

// Options 服务器配置
type Options struct {
    Addr           string
    TLSConfig      *tls.Config
    ConnectTimeout time.Duration
    MaxPacketSize  int

    // 存储（不传则使用内存实现）
    sessionStore  store.SessionStore
    messageStore  store.MessageStore
    retainedStore store.RetainedStore

    // 认证
    authenticator auth.Authenticator

    // 插件
    pluginRegistry *plugin.Registry

    // QoS 配置
    qosOpts []broker.QoSOption

    // 基础设施
    logger  logger.Logger
    metrics metrics.Metrics
}

func defaultOptions() Options {
    return Options{
        Addr:           ":1883",
        ConnectTimeout: 10 * time.Second,
        MaxPacketSize:  256 * 1024, // 256KB
        sessionStore:   memory.NewSessionStore(),
        messageStore:   memory.NewMessageStore(),
        retainedStore:  memory.NewRetainedStore(),
        authenticator:  auth.NewNoopAuthenticator(),
        pluginRegistry: plugin.NewRegistry(logger.Default()),
        logger:         logger.Default(),
        metrics:        metrics.Default(),
    }
}

type Option func(*Options)

func WithAddr(addr string) Option {
    return func(o *Options) { o.Addr = addr }
}

func WithTLS(cfg *tls.Config) Option {
    return func(o *Options) { o.TLSConfig = cfg }
}

func WithTLSFromFiles(certFile, keyFile string) Option {
    return func(o *Options) {
        cert, err := tls.LoadX509KeyPair(certFile, keyFile)
        if err != nil {
            panic("shark-mqtt: failed to load tls cert: " + err.Error())
        }
        o.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
    }
}

func WithConnectTimeout(d time.Duration) Option {
    return func(o *Options) { o.ConnectTimeout = d }
}

func WithMaxPacketSize(size int) Option {
    return func(o *Options) { o.MaxPacketSize = size }
}

func WithSessionStore(s store.SessionStore) Option {
    return func(o *Options) { o.sessionStore = s }
}

func WithMessageStore(s store.MessageStore) Option {
    return func(o *Options) { o.messageStore = s }
}

func WithRetainedStore(s store.RetainedStore) Option {
    return func(o *Options) { o.retainedStore = s }
}

func WithAuthenticator(a auth.Authenticator) Option {
    return func(o *Options) { o.authenticator = a }
}

func WithPlugin(p plugin.MQTTPlugin) Option {
    return func(o *Options) { _ = o.pluginRegistry.Register(p) }
}

func WithLogger(l logger.Logger) Option {
    return func(o *Options) { o.logger = l }
}

func WithMetrics(m metrics.Metrics) Option {
    return func(o *Options) { o.metrics = m }
}
```

---

## 协议层

### protocol/packets.go

```go
// protocol/packets.go

package protocol

// Packet 所有 MQTT 报文的基础接口
type Packet interface {
    Type() PacketType
    // Version 返回适用的 MQTT 版本
    Version() Version
}

// Version MQTT 协议版本
type Version uint8

const (
    Version311 Version = 4  // MQTT 3.1.1 协议级别
    Version500 Version = 5  // MQTT 5.0 协议级别
)

// PacketType 报文类型
type PacketType uint8

const (
    CONNECT     PacketType = 1
    CONNACK     PacketType = 2
    PUBLISH     PacketType = 3
    PUBACK      PacketType = 4
    PUBREC      PacketType = 5
    PUBREL      PacketType = 6
    PUBCOMP     PacketType = 7
    SUBSCRIBE   PacketType = 8
    SUBACK      PacketType = 9
    UNSUBSCRIBE PacketType = 10
    UNSUBACK    PacketType = 11
    PINGREQ     PacketType = 12
    PINGRESP    PacketType = 13
    DISCONNECT  PacketType = 14
    AUTH        PacketType = 15 // MQTT 5.0 新增
)

// QoSLevel QoS 等级
type QoSLevel uint8

const (
    QoS0 QoSLevel = 0 // At most once
    QoS1 QoSLevel = 1 // At least once
    QoS2 QoSLevel = 2 // Exactly once
)

// ConnectPacket CONNECT 报文
type ConnectPacket struct {
    ProtoVersion Version
    ClientID     string
    CleanSession bool   // 3.1.1: CleanSession / 5.0: CleanStart
    KeepAlive    uint16

    // 认证信息
    Username string
    Password []byte

    // 遗嘱消息
    Will *WillMessage

    // MQTT 5.0 专有
    Properties *ConnectProperties
}

func (p *ConnectPacket) Type() PacketType { return CONNECT }
func (p *ConnectPacket) Version() Version { return p.ProtoVersion }

// WillMessage 遗嘱消息
type WillMessage struct {
    Topic   string
    Payload []byte
    QoS     QoSLevel
    Retain  bool

    // MQTT 5.0 专有
    DelayInterval uint32
    Properties    *WillProperties
}

// PublishPacket PUBLISH 报文
type PublishPacket struct {
    ProtoVersion Version
    PacketID     uint16 // QoS > 0 时有效
    Topic        string
    Payload      []byte
    QoS          QoSLevel
    Retain       bool
    Dup          bool // 重传标志

    // MQTT 5.0 专有
    Properties *PublishProperties
}

func (p *PublishPacket) Type() PacketType { return PUBLISH }
func (p *PublishPacket) Version() Version { return p.ProtoVersion }

// SubscribePacket SUBSCRIBE 报文
type SubscribePacket struct {
    ProtoVersion Version
    PacketID     uint16
    Topics       []TopicFilter

    // MQTT 5.0 专有
    Properties *SubscribeProperties
}

type TopicFilter struct {
    Filter string
    QoS    QoSLevel
    // MQTT 5.0 选项
    NoLocal           bool
    RetainAsPublished bool
    RetainHandling    uint8
}

func (p *SubscribePacket) Type() PacketType { return SUBSCRIBE }
func (p *SubscribePacket) Version() Version { return p.ProtoVersion }

// UnsubscribePacket UNSUBSCRIBE 报文
type UnsubscribePacket struct {
    ProtoVersion Version
    PacketID     uint16
    Topics       []string

    Properties *UnsubscribeProperties
}

func (p *UnsubscribePacket) Type() PacketType { return UNSUBSCRIBE }
func (p *UnsubscribePacket) Version() Version { return p.ProtoVersion }

// 简单报文（无需额外字段）
type (
    PubAckPacket  struct{ ProtoVersion Version; PacketID uint16; Properties *AckProperties }
    PubRecPacket  struct{ ProtoVersion Version; PacketID uint16; Properties *AckProperties }
    PubRelPacket  struct{ ProtoVersion Version; PacketID uint16; Properties *AckProperties }
    PubCompPacket struct{ ProtoVersion Version; PacketID uint16; Properties *AckProperties }
    PingReqPacket struct{ ProtoVersion Version }
    PingRespPacket struct{ ProtoVersion Version }
    DisconnectPacket struct {
        ProtoVersion Version
        ReasonCode   uint8  // MQTT 5.0 断开原因码
        Properties   *DisconnectProperties
    }
    AuthPacket struct { // MQTT 5.0 专有
        ProtoVersion Version
        ReasonCode   uint8
        Properties   *AuthProperties
    }
)

func (p *PubAckPacket) Type() PacketType   { return PUBACK }
func (p *PubRecPacket) Type() PacketType   { return PUBREC }
func (p *PubRelPacket) Type() PacketType   { return PUBREL }
func (p *PubCompPacket) Type() PacketType  { return PUBCOMP }
func (p *PingReqPacket) Type() PacketType  { return PINGREQ }
func (p *PingRespPacket) Type() PacketType { return PINGRESP }
func (p *DisconnectPacket) Type() PacketType { return DISCONNECT }
func (p *AuthPacket) Type() PacketType     { return AUTH }

func (p *PubAckPacket) Version() Version   { return p.ProtoVersion }
func (p *PubRecPacket) Version() Version   { return p.ProtoVersion }
func (p *PubRelPacket) Version() Version   { return p.ProtoVersion }
func (p *PubCompPacket) Version() Version  { return p.ProtoVersion }
func (p *PingReqPacket) Version() Version  { return p.ProtoVersion }
func (p *PingRespPacket) Version() Version { return p.ProtoVersion }
func (p *DisconnectPacket) Version() Version { return p.ProtoVersion }
func (p *AuthPacket) Version() Version     { return p.ProtoVersion }

// ConnAckReturnCode CONNACK 返回码（3.1.1）/ 原因码（5.0）
type ConnAckReturnCode uint8

const (
    ConnAckAccepted              ConnAckReturnCode = 0x00
    ConnAckUnacceptableVersion   ConnAckReturnCode = 0x01
    ConnAckIdentifierRejected    ConnAckReturnCode = 0x02
    ConnAckServerUnavailable     ConnAckReturnCode = 0x03
    ConnAckBadCredentials        ConnAckReturnCode = 0x04
    ConnAckNotAuthorized         ConnAckReturnCode = 0x05
)

// ConnectReturnCode 将内部错误映射到 MQTT 返回码
func ConnectReturnCode(err error) ConnAckReturnCode {
    switch err {
    case errs.ErrAuthFailed:
        return ConnAckBadCredentials
    case errs.ErrNotAuthorized:
        return ConnAckNotAuthorized
    case errs.ErrClientIDEmpty:
        return ConnAckIdentifierRejected
    default:
        return ConnAckServerUnavailable
    }
}
```

### protocol/codec.go

```go
// protocol/codec.go

package protocol

import (
    "io"
    "net"

    "github.com/yourorg/shark-mqtt/errs"
)

// Codec MQTT 编解码器
// 自动检测协议版本（3.1.1 / 5.0），选择对应解码器
type Codec struct {
    maxPacketSize int
}

func NewCodec(maxPacketSize int) *Codec {
    return &Codec{maxPacketSize: maxPacketSize}
}

// DecodeConnect 解析首个 CONNECT 报文
// 根据报文中的协议级别字段自动选择解码器
func (c *Codec) DecodeConnect(r io.Reader) (*ConnectPacket, error) {
    // 读取固定头
    header, err := c.readFixedHeader(r)
    if err != nil {
        return nil, err
    }
    if PacketType(header.typeAndFlags>>4) != CONNECT {
        return nil, errs.ErrInvalidPacket
    }

    // 读取可变头 + 载荷
    payload, err := c.readPayload(r, header.remainingLength)
    if err != nil {
        return nil, err
    }

    // 读取协议名称和版本
    protoName, pos, err := readString(payload, 0)
    if err != nil {
        return nil, err
    }
    if protoName != "MQTT" && protoName != "MQIsdp" {
        return nil, errs.ErrInvalidPacket
    }
    if pos >= len(payload) {
        return nil, errs.ErrIncomplete
    }

    version := Version(payload[pos])
    switch version {
    case Version311:
        return decodeConnectV311(payload)
    case Version500:
        return decodeConnectV500(payload)
    default:
        return nil, errs.ErrUnsupportedVersion
    }
}

// Decode 解析后续报文（CONNECT 之后的所有报文）
// sess 用于确定协议版本，选择对应解码器
func (c *Codec) Decode(r io.Reader, version Version) (Packet, error) {
    header, err := c.readFixedHeader(r)
    if err != nil {
        return nil, err
    }

    if header.remainingLength > c.maxPacketSize {
        return nil, errs.ErrPacketTooLarge
    }

    payload, err := c.readPayload(r, header.remainingLength)
    if err != nil {
        return nil, err
    }

    pktType := PacketType(header.typeAndFlags >> 4)
    flags := header.typeAndFlags & 0x0F

    switch version {
    case Version311:
        return decodeV311(pktType, flags, payload)
    case Version500:
        return decodeV500(pktType, flags, payload)
    default:
        return nil, errs.ErrUnsupportedVersion
    }
}

// Encode 序列化报文为字节
func (c *Codec) Encode(pkt Packet) ([]byte, error) {
    switch pkt.Version() {
    case Version311:
        return encodeV311(pkt)
    case Version500:
        return encodeV500(pkt)
    default:
        return nil, errs.ErrUnsupportedVersion
    }
}

// SendConnAck 直接写入 CONNACK 报文（连接建立阶段，会话未初始化）
func (c *Codec) SendConnAck(w io.Writer, code ConnAckReturnCode, sessionPresent bool) error {
    sp := byte(0)
    if sessionPresent {
        sp = 1
    }
    _, err := w.Write([]byte{
        0x20,       // CONNACK 固定头
        0x02,       // 剩余长度
        sp,         // 会话存在标志
        byte(code), // 返回码
    })
    return err
}

type fixedHeader struct {
    typeAndFlags    byte
    remainingLength int
}

func (c *Codec) readFixedHeader(r io.Reader) (fixedHeader, error) {
    buf := make([]byte, 1)
    if _, err := io.ReadFull(r, buf); err != nil {
        return fixedHeader{}, err
    }
    h := fixedHeader{typeAndFlags: buf[0]}

    // 读取剩余长度（变长编码，最多 4 字节）
    multiplier := 1
    for i := 0; i < 4; i++ {
        if _, err := io.ReadFull(r, buf); err != nil {
            return fixedHeader{}, err
        }
        h.remainingLength += int(buf[0]&127) * multiplier
        if buf[0]&128 == 0 {
            break
        }
        multiplier *= 128
        if i == 3 {
            return fixedHeader{}, errs.ErrInvalidPacket
        }
    }
    return h, nil
}

func (c *Codec) readPayload(r io.Reader, length int) ([]byte, error) {
    if length == 0 {
        return nil, nil
    }
    buf := make([]byte, length)
    if _, err := io.ReadFull(r, buf); err != nil {
        return nil, err
    }
    return buf, nil
}
```

---

## Broker 核心层

### broker/broker.go

```go
// broker/broker.go

package broker

import (
    "context"
    "net"
    "sync"

    "github.com/yourorg/shark-mqtt/auth"
    "github.com/yourorg/shark-mqtt/errs"
    "github.com/yourorg/shark-mqtt/infra/logger"
    "github.com/yourorg/shark-mqtt/infra/metrics"
    "github.com/yourorg/shark-mqtt/protocol"
    "github.com/yourorg/shark-mqtt/session"
    "github.com/yourorg/shark-mqtt/store"
)

// Broker MQTT 核心业务编排层
//
// 设计约定：
//   - Broker 不持有任何 net.Conn，通过 MQTTSession 操作连接
//   - 所有网络操作通过 session.MQTTSession 的方法进行
//   - Broker 可以在无网络环境下进行单元测试
type Broker struct {
    topics    *TopicTree
    qos       *QoSEngine
    will      *WillHandler
    registry  *session.SessionRegistry

    sessionStore  store.SessionStore
    messageStore  store.MessageStore
    retainedStore store.RetainedStore
    authenticator auth.Authenticator

    logger  logger.Logger
    metrics metrics.Metrics

    mu   sync.RWMutex
    opts brokerOptions
}

func New(opts ...Option) *Broker {
    o := defaultBrokerOptions()
    for _, opt := range opts {
        opt(&o)
    }
    return &Broker{
        topics:        NewTopicTree(),
        qos:           NewQoSEngine(o.qosOpts...),
        will:          NewWillHandler(),
        registry:      session.NewSessionRegistry(o.sessionStore),
        sessionStore:  o.sessionStore,
        messageStore:  o.messageStore,
        retainedStore: o.retainedStore,
        authenticator: o.authenticator,
        logger:        o.logger,
        metrics:       o.metrics,
        opts:          o,
    }
}

// Start 启动 Broker 内部子系统
func (b *Broker) Start() {
    b.qos.Start()
}

// Stop 停止 Broker 内部子系统
func (b *Broker) Stop() {
    b.qos.Stop()
    b.registry.CloseAll(session.CloseReasonServerShut)
}

// ConnectResult CONNECT 处理结果
type ConnectResult struct {
    SessionPresent bool
}

// HandleConnect 处理 CONNECT 报文
// 返回已建立的会话和连接结果
func (b *Broker) HandleConnect(
    conn net.Conn,
    pkt *protocol.ConnectPacket,
) (*session.MQTTSession, *ConnectResult, error) {

    // 1. 认证
    authReq := &auth.Request{
        ClientID:   pkt.ClientID,
        Username:   pkt.Username,
        Password:   pkt.Password,
        RemoteAddr: conn.RemoteAddr().String(),
    }
    if !b.authenticator.Authenticate(context.Background(), authReq) {
        b.metrics.IncAuthFailures()
        return nil, nil, errs.ErrAuthFailed
    }

    // 2. 会话建立（持久会话恢复或新建）
    sessResult, err := b.registry.Connect(conn, pkt)
    if err != nil {
        return nil, nil, err
    }

    // 3. 恢复持久会话：重发离线期间收到的 QoS1/2 消息
    if sessResult.SessionPresent {
        b.redeliverOfflineMessages(sessResult.Session)
    }

    // 4. 发送订阅匹配的保留消息
    b.deliverRetainedOnSubscribe(sessResult.Session)

    b.logger.Info("mqtt client connected",
        "clientID", pkt.ClientID,
        "sessionPresent", sessResult.SessionPresent,
        "cleanSession", pkt.CleanSession,
    )

    return sessResult.Session, &ConnectResult{
        SessionPresent: sessResult.SessionPresent,
    }, nil
}

// Dispatch 分发 MQTT 报文到对应处理器
// 返回 true 表示连接应该关闭（收到 DISCONNECT 报文）
func (b *Broker) Dispatch(sess *session.MQTTSession, pkt protocol.Packet) bool {
    switch p := pkt.(type) {
    case *protocol.PublishPacket:
        if err := b.HandlePublish(sess, p); err != nil {
            b.logger.Warn("mqtt handle publish error",
                "clientID", sess.ClientID(),
                "topic", p.Topic,
                "err", err,
            )
        }
    case *protocol.SubscribePacket:
        if err := b.HandleSubscribe(sess, p); err != nil {
            b.logger.Warn("mqtt handle subscribe error",
                "clientID", sess.ClientID(),
                "err", err,
            )
        }
    case *protocol.UnsubscribePacket:
        if err := b.HandleUnsubscribe(sess, p); err != nil {
            b.logger.Warn("mqtt handle unsubscribe error",
                "clientID", sess.ClientID(),
                "err", err,
            )
        }
    case *protocol.PubAckPacket:
        b.qos.AckQoS1(sess.ClientID(), p.PacketID)
    case *protocol.PubRecPacket:
        b.qos.AckPubRec(sess.ClientID(), p.PacketID)
    case *protocol.PubRelPacket:
        // 客户端确认收到 PUBREC，发送 PUBCOMP
        _ = sess.SendPubComp(p.PacketID)
    case *protocol.PubCompPacket:
        b.qos.AckPubComp(sess.ClientID(), p.PacketID)
    case *protocol.PingReqPacket:
        _ = sess.SendPingResp()
    case *protocol.DisconnectPacket:
        b.handleGracefulDisconnect(sess, p)
        return true // 关闭连接
    }
    return false
}

// HandlePublish 处理 PUBLISH 报文
func (b *Broker) HandlePublish(sess *session.MQTTSession, pkt *protocol.PublishPacket) error {
    msg := &store.Message{
        Topic:   pkt.Topic,
        Payload: pkt.Payload,
        QoS:     uint8(pkt.QoS),
        Retain:  pkt.Retain,
    }

    b.metrics.IncMessagesPublished(pkt.Topic, uint8(pkt.QoS))

    // 处理保留消息
    if pkt.Retain {
        if len(pkt.Payload) == 0 {
            // 空 payload 表示删除保留消息
            _ = b.retainedStore.Delete(pkt.Topic)
        } else {
            _ = b.retainedStore.Set(pkt.Topic, msg)
        }
    }

    // 路由到所有匹配的订阅者
    subscribers := b.topics.Match(pkt.Topic)
    for _, sub := range subscribers {
        b.deliverToSubscriber(sub, msg)
    }

    // QoS 确认
    switch pkt.QoS {
    case protocol.QoS1:
        return sess.SendPubAck(pkt.PacketID)
    case protocol.QoS2:
        return sess.SendPubRec(pkt.PacketID)
    }
    return nil
}

// HandleSubscribe 处理 SUBSCRIBE 报文
func (b *Broker) HandleSubscribe(sess *session.MQTTSession, pkt *protocol.SubscribePacket) error {
    results := make([]session.SubResult, len(pkt.Topics))

    for i, tf := range pkt.Topics {
        // 添加到主题树
        b.topics.Subscribe(tf.Filter, tf.QoS, sess)
        results[i] = session.SubResult{
            QoS:     tf.QoS,
            Granted: true,
        }

        // 发送匹配的保留消息
        retained, err := b.retainedStore.Get(tf.Filter)
        if err == nil {
            for _, msg := range retained {
                b.deliverToSubscriber(sess, msg)
            }
        }

        b.logger.Debug("mqtt subscribe",
            "clientID", sess.ClientID(),
            "filter", tf.Filter,
            "qos", tf.QoS,
        )
    }

    return sess.SendSubAck(pkt.PacketID, results)
}

// HandleUnsubscribe 处理 UNSUBSCRIBE 报文
func (b *Broker) HandleUnsubscribe(sess *session.MQTTSession, pkt *protocol.UnsubscribePacket) error {
    for _, topic := range pkt.Topics {
        b.topics.Unsubscribe(topic, sess.ClientID())
    }
    return sess.SendUnsubAck(pkt.PacketID)
}

// HandleDisconnect 处理连接断开（网络错误或超时）
func (b *Broker) HandleDisconnect(sess *session.MQTTSession, reason session.CloseReason) {
    // 非正常断开：触发遗嘱消息
    if reason != session.CloseReasonNormal {
        if will := sess.Will(); will != nil {
            b.will.Trigger(b, will)
        }
    }

    // 从主题树移除订阅（CleanSession=true 时）
    if sess.CleanSession() {
        b.topics.RemoveAll(sess.ClientID())
    }

    // 通知注册表更新会话状态
    b.registry.Disconnect(sess.ClientID(), sess.CleanSession(), reason)

    b.metrics.DecConnections()
}

func (b *Broker) handleGracefulDisconnect(
    sess *session.MQTTSession,
    pkt *protocol.DisconnectPacket,
) {
    // 正常断开：不触发遗嘱消息
    if sess.CleanSession() {
        b.topics.RemoveAll(sess.ClientID())
    }
    b.registry.Disconnect(sess.ClientID(), sess.CleanSession(), session.CloseReasonNormal)
    b.metrics.DecConnections()
}

// deliverToSubscriber 向单个订阅者投递消息
func (b *Broker) deliverToSubscriber(sub *session.MQTTSession, msg *store.Message) {
    // 取订阅 QoS 和消息 QoS 的最小值
    subQoS := sub.SubscriptionQoS(msg.Topic)
    effectiveQoS := protocol.QoSLevel(msg.QoS)
    if subQoS < effectiveQoS {
        effectiveQoS = subQoS
    }

    if !sub.IsOnline() {
        // 离线客户端：QoS1/2 消息存入离线队列
        if effectiveQoS > protocol.QoS0 && !sub.CleanSession() {
            _ = b.messageStore.Enqueue(
                context.Background(),
                sub.ClientID(),
                msg,
            )
        }
        return
    }

    switch effectiveQoS {
    case protocol.QoS0:
        _ = sub.SendPublish(msg, 0)
    case protocol.QoS1, protocol.QoS2:
        b.qos.Enqueue(sub, msg, effectiveQoS)
    }
}

// redeliverOfflineMessages 重连后重发离线期间收到的消息
func (b *Broker) redeliverOfflineMessages(sess *session.MQTTSession) {
    msgs, err := b.messageStore.Dequeue(context.Background(), sess.ClientID())
    if err != nil {
        b.logger.Warn("mqtt redeliver offline messages failed",
            "clientID", sess.ClientID(),
            "err", err,
        )
        return
    }
    for _, msg := range msgs {
        b.qos.Enqueue(sess, msg, protocol.QoSLevel(msg.QoS))
    }
}

// deliverRetainedOnSubscribe 向新订阅发送保留消息
func (b *Broker) deliverRetainedOnSubscribe(sess *session.MQTTSession) {
    sess.ForEachSubscription(func(filter string, qos protocol.QoSLevel) bool {
        retained, err := b.retainedStore.Get(filter)
        if err != nil {
            return true
        }
        for _, msg := range retained {
            b.deliverToSubscriber(sess, msg)
        }
        return true
    })
}
```

### broker/topic_tree.go

```go
// broker/topic_tree.go

package broker

import (
    "strings"
    "sync"

    "github.com/yourorg/shark-mqtt/protocol"
    "github.com/yourorg/shark-mqtt/session"
)

// TopicTree 基于 Trie 的 MQTT 主题订阅树
//
// 通配符规则（MQTT 规范）：
//   +  匹配单层：sensor/+/temp 匹配 sensor/room1/temp
//   #  匹配多层（必须在末尾）：sensor/# 匹配 sensor/room1/temp/celsius
//   $  系统主题不被 # 和 + 匹配（$SYS/# 除外）
type TopicTree struct {
    mu   sync.RWMutex
    root *topicNode
}

type topicNode struct {
    // segment → 子节点
    children map[string]*topicNode
    // clientID → 订阅信息
    subscribers map[string]*topicSubscriber
}

type topicSubscriber struct {
    session *session.MQTTSession
    qos     protocol.QoSLevel
}

func NewTopicTree() *TopicTree {
    return &TopicTree{
        root: newTopicNode(),
    }
}

func newTopicNode() *topicNode {
    return &topicNode{
        children:    make(map[string]*topicNode),
        subscribers: make(map[string]*topicSubscriber),
    }
}

// Subscribe 添加或更新订阅
func (t *TopicTree) Subscribe(filter string, qos protocol.QoSLevel, sess *session.MQTTSession) {
    t.mu.Lock()
    defer t.mu.Unlock()

    node := t.root
    for _, seg := range splitFilter(filter) {
        if node.children[seg] == nil {
            node.children[seg] = newTopicNode()
        }
        node = node.children[seg]
    }
    node.subscribers[sess.ClientID()] = &topicSubscriber{
        session: sess,
        qos:     qos,
    }
}

// Unsubscribe 移除指定主题的订阅
func (t *TopicTree) Unsubscribe(filter string, clientID string) {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.unsubscribeNode(t.root, splitFilter(filter), 0, clientID)
}

// Match 查找匹配指定主题的所有订阅者
// 遵循 MQTT 通配符规则，去重（clientID 级别）
func (t *TopicTree) Match(topic string) []*session.MQTTSession {
    t.mu.RLock()
    defer t.mu.RUnlock()

    // 系统主题不被普通通配符匹配
    isSys := strings.HasPrefix(topic, "$")

    seen := make(map[string]*session.MQTTSession)
    t.matchNode(t.root, splitFilter(topic), 0, isSys, seen)

    result := make([]*session.MQTTSession, 0, len(seen))
    for _, s := range seen {
        result = append(result, s)
    }
    return result
}

// RemoveAll 移除某个客户端的所有订阅（CleanSession=true 断开时调用）
func (t *TopicTree) RemoveAll(clientID string) {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.removeAllFromNode(t.root, clientID)
}

// Count 返回当前节点总订阅数（用于监控）
func (t *TopicTree) Count() int {
    t.mu.RLock()
    defer t.mu.RUnlock()
    return t.countNode(t.root)
}

func (t *TopicTree) matchNode(
    node *topicNode,
    segments []string,
    depth int,
    isSys bool,
    result map[string]*session.MQTTSession,
) {
    if depth == len(segments) {
        t.collectSubscribers(node, result)
        // # 也匹配空后缀
        if hashNode, ok := node.children["#"]; ok {
            t.collectSubscribers(hashNode, result)
        }
        return
    }

    seg := segments[depth]

    // 精确匹配
    if child, ok := node.children[seg]; ok {
        t.matchNode(child, segments, depth+1, isSys, result)
    }

    // + 单层通配符（系统主题的首层不匹配）
    if !(isSys && depth == 0) {
        if child, ok := node.children["+"]; ok {
            t.matchNode(child, segments, depth+1, isSys, result)
        }
    }

    // # 多层通配符（系统主题的首层不匹配）
    if !(isSys && depth == 0) {
        if child, ok := node.children["#"]; ok {
            t.collectSubscribers(child, result)
        }
    }
}

func (t *TopicTree) collectSubscribers(
    node *topicNode,
    result map[string]*session.MQTTSession,
) {
    for clientID, sub := range node.subscribers {
        if _, exists := result[clientID]; !exists {
            result[clientID] = sub.session
        }
    }
}

func (t *TopicTree) unsubscribeNode(
    node *topicNode,
    segments []string,
    depth int,
    clientID string,
) bool {
    if depth == len(segments) {
        delete(node.subscribers, clientID)
        return len(node.subscribers) == 0 && len(node.children) == 0
    }
    seg := segments[depth]
    child, ok := node.children[seg]
    if !ok {
        return false
    }
    if t.unsubscribeNode(child, segments, depth+1, clientID) {
        delete(node.children, seg)
    }
    return len(node.subscribers) == 0 && len(node.children) == 0
}

func (t *TopicTree) removeAllFromNode(node *topicNode, clientID string) {
    delete(node.subscribers, clientID)
    for _, child := range node.children {
        t.removeAllFromNode(child, clientID)
    }
}

func (t *TopicTree) countNode(node *topicNode) int {
    count := len(node.subscribers)
    for _, child := range node.children {
        count += t.countNode(child)
    }
    return count
}

func splitFilter(filter string) []string {
    return strings.Split(filter, "/")
}
```

### broker/qos_engine.go

```go
// broker/qos_engine.go

package broker

import (
    "context"
    "sync"
    "time"

    "github.com/yourorg/shark-mqtt/errs"
    "github.com/yourorg/shark-mqtt/infra/logger"
    "github.com/yourorg/shark-mqtt/infra/metrics"
    "github.com/yourorg/shark-mqtt/protocol"
    "github.com/yourorg/shark-mqtt/session"
    "github.com/yourorg/shark-mqtt/store"
)

// QoSEngine QoS 1/2 状态机引擎
//
// QoS 1 状态流转：
//   Enqueue → SENT → [PUBACK] → DONE
//
// QoS 2 状态流转：
//   Enqueue → SENT → [PUBREC] → PUBREL_SENT → [PUBCOMP] → DONE
//
// 超时重发：retryInterval 内未收到确认则重发，超过 maxRetries 则断开连接
type QoSEngine struct {
    mu       sync.Mutex
    inflight map[string]map[uint16]*inflightMsg // clientID → packetID → msg

    retryInterval time.Duration
    maxRetries    int
    maxInflight   int

    logger  logger.Logger
    metrics metrics.Metrics

    ctx    context.Context
    cancel context.CancelFunc
    wg     sync.WaitGroup
}

type inflightState uint8

const (
    inflightSent        inflightState = iota // QoS1: 已发，等待 PUBACK
    inflightPubRecvd                          // QoS2: 已收 PUBREC
    inflightPubRelSent                        // QoS2: 已发 PUBREL，等待 PUBCOMP
)

type inflightMsg struct {
    msg      *store.Message
    sess     *session.MQTTSession
    packetID uint16
    qos      protocol.QoSLevel
    state    inflightState
    sentAt   time.Time
    retries  int
}

func NewQoSEngine(opts ...QoSOption) *QoSEngine {
    o := defaultQoSOptions()
    for _, opt := range opts {
        opt(&o)
    }
    ctx, cancel := context.WithCancel(context.Background())
    return &QoSEngine{
        inflight:      make(map[string]map[uint16]*inflightMsg),
        retryInterval: o.RetryInterval,
        maxRetries:    o.MaxRetries,
        maxInflight:   o.MaxInflight,
        logger:        o.Logger,
        metrics:       o.Metrics,
        ctx:           ctx,
        cancel:        cancel,
    }
}

func (e *QoSEngine) Start() {
    e.wg.Add(1)
    go func() {
        defer e.wg.Done()
        e.retryLoop()
    }()
}

func (e *QoSEngine) Stop() {
    e.cancel()
    e.wg.Wait()
}

// Enqueue 入队消息，开始 QoS 1/2 流程
func (e *QoSEngine) Enqueue(
    sess *session.MQTTSession,
    msg *store.Message,
    qos protocol.QoSLevel,
) error {
    e.mu.Lock()
    defer e.mu.Unlock()

    clientID := sess.ClientID()
    if e.inflight[clientID] == nil {
        e.inflight[clientID] = make(map[uint16]*inflightMsg)
    }

    // 背压控制：超过最大在途数拒绝新消息
    if len(e.inflight[clientID]) >= e.maxInflight {
        e.metrics.IncInflightDropped(clientID)
        return errs.ErrInflightFull
    }

    packetID := sess.NextPacketID()
    im := &inflightMsg{
        msg:      msg,
        sess:     sess,
        packetID: packetID,
        qos:      qos,
        state:    inflightSent,
        sentAt:   time.Now(),
    }

    e.inflight[clientID][packetID] = im

    if err := sess.SendPublish(msg, packetID); err != nil {
        delete(e.inflight[clientID], packetID)
        return err
    }

    e.metrics.IncInflight(clientID)
    return nil
}

// AckQoS1 处理 PUBACK，完成 QoS1 流程
func (e *QoSEngine) AckQoS1(clientID string, packetID uint16) {
    e.mu.Lock()
    defer e.mu.Unlock()
    if msgs, ok := e.inflight[clientID]; ok {
        if _, ok := msgs[packetID]; ok {
            delete(msgs, packetID)
            e.metrics.DecInflight(clientID)
        }
    }
}

// AckPubRec 处理 PUBREC（QoS2 第2步），发送 PUBREL
func (e *QoSEngine) AckPubRec(clientID string, packetID uint16) {
    e.mu.Lock()
    defer e.mu.Unlock()
    msgs, ok := e.inflight[clientID]
    if !ok {
        return
    }
    im, ok := msgs[packetID]
    if !ok {
        return
    }
    im.state = inflightPubRecvd
    im.sentAt = time.Now()
    im.retries = 0
    if err := im.sess.SendPubRel(packetID); err != nil {
        e.logger.Warn("mqtt send pubrel failed",
            "clientID", clientID,
            "packetID", packetID,
            "err", err,
        )
        return
    }
    im.state = inflightPubRelSent
}

// AckPubComp 处理 PUBCOMP，完成 QoS2 流程
func (e *QoSEngine) AckPubComp(clientID string, packetID uint16) {
    e.mu.Lock()
    defer e.mu.Unlock()
    if msgs, ok := e.inflight[clientID]; ok {
        if _, ok := msgs[packetID]; ok {
            delete(msgs, packetID)
            e.metrics.DecInflight(clientID)
        }
    }
}

// RemoveClient 客户端断开时处理在途消息
func (e *QoSEngine) RemoveClient(clientID string, clean bool) {
    if !clean {
        return // CleanSession=false：保留在途记录，等待重连
    }
    e.mu.Lock()
    defer e.mu.Unlock()
    if msgs, ok := e.inflight[clientID]; ok {
        e.metrics.DecInflightBatch(clientID, len(msgs))
        delete(e.inflight, clientID)
    }
}

// retryLoop 定期检查超时的在途消息并重发
func (e *QoSEngine) retryLoop() {
    ticker := time.NewTicker(e.retryInterval)
    defer ticker.Stop()

    for {
        select {
        case <-e.ctx.Done():
            return
        case <-ticker.C:
            e.processRetry()
        }
    }
}

func (e *QoSEngine) processRetry() {
    e.mu.Lock()
    defer e.mu.Unlock()

    now := time.Now()
    for clientID, msgs := range e.inflight {
        for packetID, im := range msgs {
            if now.Sub(im.sentAt) < e.retryInterval {
                continue
            }

            // 超过最大重试次数：强制断开连接
            if im.retries >= e.maxRetries {
                e.logger.Warn("mqtt qos max retries exceeded, closing session",
                    "clientID", clientID,
                    "packetID", packetID,
                    "qos", im.qos,
                )
                _ = im.sess.Close(session.CloseReasonTimeout)
                delete(msgs, packetID)
                e.metrics.DecInflight(clientID)
                continue
            }

            // 重发
            im.retries++
            im.sentAt = now
            e.metrics.IncRetries(clientID)

            switch im.state {
            case inflightSent:
                _ = im.sess.SendPublish(im.msg, im.packetID)
            case inflightPubRelSent:
                _ = im.sess.SendPubRel(im.packetID)
            }
        }
    }
}
```

---

## Session 层

### session/types.go

```go
// session/types.go

package session

import "time"

// State MQTT 会话状态
type State uint8

const (
    StateOnline  State = iota // 有活跃连接
    StateOffline              // 无连接，会话持久保留（CleanSession=false）
    StateClosed               // 会话已销毁
)

// CloseReason 关闭原因
type CloseReason uint8

const (
    CloseReasonNormal     CloseReason = iota // 客户端正常 DISCONNECT
    CloseReasonTimeout                        // KeepAlive 超时
    CloseReasonError                          // 网络或协议错误
    CloseReasonKicked                         // 被新连接踢下线
    CloseReasonServerShut                     // 服务器关闭
    CloseReasonAuthFailed                     // 认证失败
)

func (r CloseReason) String() string {
    names := [...]string{
        "Normal", "Timeout", "Error", "Kicked", "ServerShut", "AuthFailed",
    }
    if int(r) < len(names) {
        return names[r]
    }
    return "Unknown"
}

// Stats 会话统计信息
type Stats struct {
    ConnectedAt   time.Time
    LastActiveAt  time.Time
    BytesReceived uint64
    BytesSent     uint64
    MessagesIn    uint64
    MessagesOut   uint64
    Subscriptions int
}

// SubResult 订阅结果
type SubResult struct {
    QoS     protocol.QoSLevel
    Granted bool
}
```

### session/session.go

```go
// session/session.go

package session

import (
    "context"
    "net"
    "sync"
    "sync/atomic"
    "time"

    "github.com/yourorg/shark-mqtt/errs"
    "github.com/yourorg/shark-mqtt/infra/logger"
    "github.com/yourorg/shark-mqtt/protocol"
    "github.com/yourorg/shark-mqtt/store"
)

// MQTTSession MQTT 会话
//
// 核心设计：连接与会话分离
//   IsClosed()  → 会话对象是否已销毁（CleanSession=true 断开后）
//   IsOnline()  → 当前是否有活跃 TCP 连接
//   Detach()    → 断开连接，保留会话状态（CleanSession=false）
//   Attach()    → 重连时绑定新连接
type MQTTSession struct {
    // MQTT 身份（创建后不变）
    clientID     string
    keepAlive    uint16
    cleanSession bool
    protoVersion protocol.Version

    // 遗嘱消息（创建时确定）
    will *protocol.WillMessage

    // 连接绑定（可热替换，原子操作）
    conn      atomic.Pointer[net.Conn]
    writeChan atomic.Pointer[chan []byte]

    // 会话状态
    state  atomic.Uint32 // State
    closed atomic.Bool

    ctx    context.Context
    cancel context.CancelFunc

    // 订阅管理（线程安全）
    subsMu        sync.RWMutex
    subscriptions map[string]protocol.QoSLevel // filter → QoS

    // 包 ID 生成（原子递增）
    nextPacketID atomic.Uint32

    // 统计（原子操作）
    connectedAt  time.Time
    lastActiveAt atomic.Int64
    bytesRecv    atomic.Uint64
    bytesSent    atomic.Uint64
    msgsIn       atomic.Uint64
    msgsOut      atomic.Uint64

    // 写队列大小
    writeQueueSize int

    logger logger.Logger
}

func NewMQTTSession(
    pkt *protocol.ConnectPacket,
    writeQueueSize int,
    log logger.Logger,
) *MQTTSession {
    ctx, cancel := context.WithCancel(context.Background())
    s := &MQTTSession{
        clientID:       pkt.ClientID,
        keepAlive:      pkt.KeepAlive,
        cleanSession:   pkt.CleanSession,
        protoVersion:   pkt.ProtoVersion,
        will:           pkt.Will,
        subscriptions:  make(map[string]protocol.QoSLevel),
        connectedAt:    time.Now(),
        writeQueueSize: writeQueueSize,
        ctx:            ctx,
        cancel:         cancel,
        logger:         log,
    }
    s.lastActiveAt.Store(time.Now().UnixNano())
    s.state.Store(uint32(StateOffline))
    return s
}

// Attach 绑定新连接（TCP 连接建立时）
func (s *MQTTSession) Attach(conn net.Conn) {
    ch := make(chan []byte, s.writeQueueSize)
    s.conn.Store(&conn)
    s.writeChan.Store(&ch)
    s.state.Store(uint32(StateOnline))
    s.connectedAt = time.Now()
    s.lastActiveAt.Store(time.Now().UnixNano())

    // 启动写协程（每次 Attach 启动一个新的，随连接生命周期）
    go s.writeLoop(conn, ch)
}

// Detach 解绑连接，保留会话状态（CleanSession=false 断开时调用）
func (s *MQTTSession) Detach() {
    s.state.Store(uint32(StateOffline))
    s.conn.Store(nil)

    // 关闭写队列，终止 writeLoop
    if chPtr := s.writeChan.Swap(nil); chPtr != nil {
        close(*chPtr)
    }
}

// Close 销毁会话（CleanSession=true 断开时调用）
func (s *MQTTSession) Close(reason CloseReason) error {
    if !s.closed.CompareAndSwap(false, true) {
        return nil // 幂等
    }
    s.state.Store(uint32(StateClosed))
    s.cancel()
    s.Detach()

    // 清理订阅
    s.subsMu.Lock()
    s.subscriptions = make(map[string]protocol.QoSLevel)
    s.subsMu.Unlock()

    return nil
}

// SendRaw 发送原始字节（投递到写队列）
func (s *MQTTSession) SendRaw(data []byte) error {
    if !s.IsOnline() {
        return errs.ErrSessionClosed
    }
    chPtr := s.writeChan.Load()
    if chPtr == nil {
        return errs.ErrSessionClosed
    }
    buf := make([]byte, len(data))
    copy(buf, data)
    select {
    case *chPtr <- buf:
        s.bytesSent.Add(uint64(len(buf)))
        s.msgsOut.Add(1)
        return nil
    default:
        return errs.ErrWriteQueueFull
    }
}

// writeLoop 连接写协程（随 Attach 启动，随 Detach 退出）
func (s *MQTTSession) writeLoop(conn net.Conn, ch <-chan []byte) {
    for data := range ch {
        if _, err := conn.Write(data); err != nil {
            s.logger.Warn("mqtt write error",
                "clientID", s.clientID,
                "err", err,
            )
            return
        }
    }
}

// ── 发送各类 MQTT 报文 ────────────────────────────────────

func (s *MQTTSession) SendPublish(msg *store.Message, packetID uint16) error {
    pkt := &protocol.PublishPacket{
        ProtoVersion: s.protoVersion,
        PacketID:     packetID,
        Topic:        msg.Topic,
        Payload:      msg.Payload,
        QoS:          protocol.QoSLevel(msg.QoS),
        Retain:       msg.Retain,
        Dup:          packetID > 0, // 重传时标记 DUP
    }
    data, err := protocol.NewCodec(0).Encode(pkt)
    if err != nil {
        return err
    }
    return s.SendRaw(data)
}

func (s *MQTTSession) SendPubAck(packetID uint16) error {
    return s.sendSimplePacket(&protocol.PubAckPacket{
        ProtoVersion: s.protoVersion,
        PacketID:     packetID,
    })
}

func (s *MQTTSession) SendPubRec(packetID uint16) error {
    return s.sendSimplePacket(&protocol.PubRecPacket{
        ProtoVersion: s.protoVersion,
        PacketID:     packetID,
    })
}

func (s *MQTTSession) SendPubRel(packetID uint16) error {
    return s.sendSimplePacket(&protocol.PubRelPacket{
        ProtoVersion: s.protoVersion,
        PacketID:     packetID,
    })
}

func (s *MQTTSession) SendPubComp(packetID uint16) error {
    return s.sendSimplePacket(&protocol.PubCompPacket{
        ProtoVersion: s.protoVersion,
        PacketID:     packetID,
    })
}

func (s *MQTTSession) SendSubAck(packetID uint16, results []SubResult) error {
    // 编码 SUBACK 并发送
    codes := make([]byte, len(results))
    for i, r := range results {
        if r.Granted {
            codes[i] = byte(r.QoS)
        } else {
            codes[i] = 0x80 // 订阅失败
        }
    }
    data := encodeSubAck(s.protoVersion, packetID, codes)
    return s.SendRaw(data)
}

func (s *MQTTSession) SendUnsubAck(packetID uint16) error {
    return s.sendSimplePacket(&protocol.PubAckPacket{
        ProtoVersion: s.protoVersion,
        PacketID:     packetID,
    })
}

func (s *MQTTSession) SendPingResp() error {
    return s.SendRaw([]byte{0xD0, 0x00}) // PINGRESP 固定报文
}

func (s *MQTTSession) sendSimplePacket(pkt protocol.Packet) error {
    data, err := protocol.NewCodec(0).Encode(pkt)
    if err != nil {
        return err
    }
    return s.SendRaw(data)
}

// ── 属性访问 ──────────────────────────────────────────────

func (s *MQTTSession) ClientID() string           { return s.clientID }
func (s *MQTTSession) KeepAlive() uint16          { return s.keepAlive }
func (s *MQTTSession) CleanSession() bool         { return s.cleanSession }
func (s *MQTTSession) ProtoVersion() protocol.Version { return s.protoVersion }
func (s *MQTTSession) Will() *protocol.WillMessage { return s.will }
func (s *MQTTSession) IsOnline() bool             { return State(s.state.Load()) == StateOnline }
func (s *MQTTSession) IsClosed() bool             { return s.closed.Load() }
func (s *MQTTSession) Ctx() context.Context       { return s.ctx }

func (s *MQTTSession) NextPacketID() uint16 {
    // 包 ID 范围 1 ~ 65535，循环使用
    for {
        id := uint16(s.nextPacketID.Add(1))
        if id == 0 {
            continue // 跳过 0
        }
        return id
    }
}

func (s *MQTTSession) UpdateLastActive() {
    s.lastActiveAt.Store(time.Now().UnixNano())
    s.msgsIn.Add(1)
}

// ── 订阅管理 ──────────────────────────────────────────────

func (s *MQTTSession) AddSubscription(filter string, qos protocol.QoSLevel) {
    s.subsMu.Lock()
    defer s.subsMu.Unlock()
    s.subscriptions[filter] = qos
}

func (s *MQTTSession) RemoveSubscription(filter string) {
    s.subsMu.Lock()
    defer s.subsMu.Unlock()
    delete(s.subscriptions, filter)
}

func (s *MQTTSession) SubscriptionQoS(topic string) protocol.QoSLevel {
    s.subsMu.RLock()
    defer s.subsMu.RUnlock()
    // 查找最佳匹配（精确匹配优先）
    if qos, ok := s.subscriptions[topic]; ok {
        return qos
    }
    return protocol.QoS0
}

func (s *MQTTSession) ForEachSubscription(fn func(filter string, qos protocol.QoSLevel) bool) {
    s.subsMu.RLock()
    defer s.subsMu.RUnlock()
    for filter, qos := range s.subscriptions {
        if !fn(filter, qos) {
            break
        }
    }
}

func (s *MQTTSession) SubscriptionCount() int {
    s.subsMu.RLock()
    defer s.subsMu.RUnlock()
    return len(s.subscriptions)
}

// Stats 返回会话统计快照
func (s *MQTTSession) Stats() Stats {
    return Stats{
        ConnectedAt:   s.connectedAt,
        LastActiveAt:  time.Unix(0, s.lastActiveAt.Load()),
        BytesReceived: s.bytesRecv.Load(),
        BytesSent:     s.bytesSent.Load(),
        MessagesIn:    s.msgsIn.Load(),
        MessagesOut:   s.msgsOut.Load(),
        Subscriptions: s.SubscriptionCount(),
    }
}
```

### session/registry.go

```go
// session/registry.go

package session

import (
    "fmt"
    "net"
    "sync"

    "github.com/yourorg/shark-mqtt/errs"
    "github.com/yourorg/shark-mqtt/infra/logger"
    "github.com/yourorg/shark-mqtt/protocol"
    "github.com/yourorg/shark-mqtt/store"
)

// SessionRegistry MQTT 会话注册表
//
// 职责分工：
//   连接维度 → 由 Manager 管理（在线会话计数、遍历）
//   会话维度 → 由 SessionRegistry 管理（持久会话跨连接存活）
//
// 两者通过 clientID 关联，不共用存储结构
type SessionRegistry struct {
    mu         sync.RWMutex
    sessions   map[string]*MQTTSession // clientID → MQTTSession

    store  store.SessionStore
    logger logger.Logger

    writeQueueSize int
}

func NewSessionRegistry(s store.SessionStore, opts ...RegistryOption) *SessionRegistry {
    o := defaultRegistryOptions()
    for _, opt := range opts {
        opt(&o)
    }
    return &SessionRegistry{
        sessions:       make(map[string]*MQTTSession),
        store:          s,
        logger:         o.logger,
        writeQueueSize: o.writeQueueSize,
    }
}

// ConnectResult 连接结果
type ConnectResult struct {
    Session        *MQTTSession
    SessionPresent bool
}

// Connect 处理 CONNECT 报文，返回会话和连接结果
//
// 处理逻辑：
//   1. 若 clientID 已有在线会话 → 踢下线（协议规定）
//   2. CleanSession=false 且存在持久会话 → 恢复
//   3. 否则 → 创建新会话
func (r *SessionRegistry) Connect(
    conn net.Conn,
    pkt *protocol.ConnectPacket,
) (*ConnectResult, error) {

    if pkt.ClientID == "" {
        return nil, errs.ErrClientIDEmpty
    }

    r.mu.Lock()
    defer r.mu.Unlock()

    sessionPresent := false

    // 踢掉同 ClientID 的已有在线会话
    if existing, ok := r.sessions[pkt.ClientID]; ok {
        if existing.IsOnline() {
            existing.Detach()
            r.logger.Info("mqtt session kicked",
                "clientID", pkt.ClientID,
                "reason", "new connection with same clientID",
            )
        }
        if pkt.CleanSession {
            // CleanSession=true：销毁旧持久会话
            _ = existing.Close(CloseReasonKicked)
            delete(r.sessions, pkt.ClientID)
        }
    }

    var sess *MQTTSession

    if !pkt.CleanSession {
        if existing, ok := r.sessions[pkt.ClientID]; ok {
            // 恢复持久会话
            sess = existing
            sessionPresent = true
            r.logger.Info("mqtt persistent session resumed",
                "clientID", pkt.ClientID,
            )
        }
    }

    if sess == nil {
        // 创建新会话
        sess = NewMQTTSession(pkt, r.writeQueueSize, r.logger)
        if !pkt.CleanSession {
            r.sessions[pkt.ClientID] = sess
        }
    }

    // 绑定连接
    sess.Attach(conn)

    if pkt.CleanSession {
        // CleanSession=true 的会话不加入 persistent map
        // 但仍需在内存中追踪（用于踢下线检测）
        r.sessions[pkt.ClientID] = sess
    }

    return &ConnectResult{
        Session:        sess,
        SessionPresent: sessionPresent,
    }, nil
}

// WasSessionPresent 查询指定 clientID 是否有持久会话（仅供日志用）
func (r *SessionRegistry) WasSessionPresent(clientID string) bool {
    r.mu.RLock()
    defer r.mu.RUnlock()
    _, ok := r.sessions[clientID]
    return ok
}

// Disconnect 处理会话断开
func (r *SessionRegistry) Disconnect(
    clientID string,
    cleanSession bool,
    reason CloseReason,
) {
    r.mu.Lock()
    defer r.mu.Unlock()

    sess, ok := r.sessions[clientID]
    if !ok {
        return
    }

    if cleanSession {
        // 销毁会话
        _ = sess.Close(reason)
        delete(r.sessions, clientID)
        r.logger.Debug("mqtt session destroyed",
            "clientID", clientID,
            "reason", reason,
        )
    } else {
        // 保留会话，只断开连接
        sess.Detach()
        r.logger.Debug("mqtt session offline",
            "clientID", clientID,
            "reason", reason,
        )
    }
}

// Get 获取会话（在线或离线）
func (r *SessionRegistry) Get(clientID string) (*MQTTSession, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    s, ok := r.sessions[clientID]
    return s, ok
}

// Count 统计信息
func (r *SessionRegistry) Count() (online, offline int) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    for _, s := range r.sessions {
        if s.IsOnline() {
            online++
        } else {
            offline++
        }
    }
    return
}

// CloseAll 关闭所有会话（服务器停止时）
func (r *SessionRegistry) CloseAll(reason CloseReason) {
    r.mu.Lock()
    defer r.mu.Unlock()
    for clientID, sess := range r.sessions {
        _ = sess.Close(reason)
        delete(r.sessions, clientID)
    }
}
```

---

## 存储层

### store/interfaces.go

```go
// store/interfaces.go

package store

import (
    "context"
    "time"
)

// Message 消息结构（存储格式）
type Message struct {
    ID        string
    Topic     string
    Payload   []byte
    QoS       uint8
    Retain    bool
    CreatedAt time.Time
}

// PersistedSession 需要持久化的会话状态
type PersistedSession struct {
    ClientID      string
    Subscriptions map[string]uint8 // filter → QoS
    CreatedAt     time.Time
    UpdatedAt     time.Time
}

// SessionStore 会话状态持久化接口
// CleanSession=false 时使用
type SessionStore interface {
    Save(ctx context.Context, s *PersistedSession) error
    Load(ctx context.Context, clientID string) (*PersistedSession, bool, error)
    Delete(ctx context.Context, clientID string) error
}

// MessageStore QoS1/2 消息持久化接口
// 用于离线消息队列（CleanSession=false 的客户端离线期间收到的消息）
type MessageStore interface {
    Enqueue(ctx context.Context, clientID string, msg *Message) error
    Dequeue(ctx context.Context, clientID string) ([]*Message, error)
    Ack(ctx context.Context, clientID string, msgID string) error
    Count(ctx context.Context, clientID string) (int, error)
}

// RetainedStore 保留消息存储接口
type RetainedStore interface {
    Set(topic string, msg *Message) error
    Get(topicFilter string) ([]*Message, error)
    Delete(topic string) error
    Count() int
}
```

---

## 认证层

### auth/auth.go

```go
// auth/auth.go

package auth

import "context"

// Request 认证请求
type Request struct {
    ClientID   string
    Username   string
    Password   []byte
    RemoteAddr string
}

// Authenticator 认证接口
type Authenticator interface {
    Authenticate(ctx context.Context, req *Request) bool
}

// NoopAuthenticator 默认实现：全部放行
type NoopAuthenticator struct{}

func NewNoopAuthenticator() *NoopAuthenticator { return &NoopAuthenticator{} }

func (n *NoopAuthenticator) Authenticate(_ context.Context, _ *Request) bool {
    return true
}

// ChainAuthenticator 链式认证：所有认证器都通过才放行
type ChainAuthenticator struct {
    auths []Authenticator
}

func NewChainAuthenticator(auths ...Authenticator) *ChainAuthenticator {
    return &ChainAuthenticator{auths: auths}
}

func (c *ChainAuthenticator) Authenticate(ctx context.Context, req *Request) bool {
    for _, a := range c.auths {
        if !a.Authenticate(ctx, req) {
            return false
        }
    }
    return true
}

// FuncAuthenticator 函数式认证器（便于内联使用）
type FuncAuthenticator struct {
    fn func(ctx context.Context, req *Request) bool
}

func NewFuncAuthenticator(fn func(ctx context.Context, req *Request) bool) *FuncAuthenticator {
    return &FuncAuthenticator{fn: fn}
}

func (f *FuncAuthenticator) Authenticate(ctx context.Context, req *Request) bool {
    return f.fn(ctx, req)
}
```

---

## 插件层

### plugin/plugin.go

```go
// plugin/plugin.go

package plugin

import (
    "github.com/yourorg/shark-mqtt/protocol"
    "github.com/yourorg/shark-mqtt/session"
)

// MQTTPlugin MQTT 专用插件接口
//
// 相比通用网络框架的 Plugin 接口，增加了 MQTT 特有的钩子：
//   OnPublish   → 消息发布前拦截（可修改或拒绝）
//   OnSubscribe → 订阅建立前拦截（可拒绝特定主题）
//
// 所有方法应快速返回，不执行阻塞操作
type MQTTPlugin interface {
    Name() string
    Priority() int

    // OnAccept 连接建立前，返回 false 立即拒绝（零资源分配）
    OnAccept(remoteAddr string) bool

    // OnConnect CONNECT 报文认证通过后调用
    OnConnect(sess *session.MQTTSession)

    // OnPublish PUBLISH 报文处理前调用
    // 返回 false 则丢弃此消息
    OnPublish(sess *session.MQTTSession, pkt *protocol.PublishPacket) bool

    // OnSubscribe SUBSCRIBE 处理前调用
    // 返回 false 则拒绝此订阅
    OnSubscribe(sess *session.MQTTSession, filter string, qos protocol.QoSLevel) bool

    // OnDisconnect 会话断开时调用
    OnDisconnect(sess *session.MQTTSession, reason session.CloseReason)
}

// BaseMQTTPlugin 默认实现，嵌入后只需覆盖关心的方法
type BaseMQTTPlugin struct{}

func (b *BaseMQTTPlugin) Name() string     { return "base" }
func (b *BaseMQTTPlugin) Priority() int    { return 100 }

func (b *BaseMQTTPlugin) OnAccept(_ string) bool { return true }
func (b *BaseMQTTPlugin) OnConnect(_ *session.MQTTSession) {}

func (b *BaseMQTTPlugin) OnPublish(
    _ *session.MQTTSession,
    _ *protocol.PublishPacket,
) bool {
    return true
}

func (b *BaseMQTTPlugin) OnSubscribe(
    _ *session.MQTTSession,
    _ string,
    _ protocol.QoSLevel,
) bool {
    return true
}

func (b *BaseMQTTPlugin) OnDisconnect(
    _ *session.MQTTSession,
    _ session.CloseReason,
) {}
```

---

## 可观测性

### infra/metrics/metrics.go

```go
// infra/metrics/metrics.go

package metrics

// Metrics MQTT 专用指标接口
type Metrics interface {
    // 连接指标
    IncConnections()
    DecConnections()
    IncRejections(reason string)
    IncAuthFailures()

    // 消息指标
    IncMessagesPublished(topic string, qos uint8)
    IncMessagesDelivered(clientID string, qos uint8)
    IncMessagesDropped(reason string)

    // QoS 指标
    IncInflight(clientID string)
    DecInflight(clientID string)
    DecInflightBatch(clientID string, count int)
    IncInflightDropped(clientID string)
    IncRetries(clientID string)

    // 会话指标
    SetOnlineSessions(count int)
    SetOfflineSessions(count int)
    SetRetainedMessages(count int)
    SetSubscriptions(count int)

    // 系统指标
    IncErrors(component string)
}
```

---

## Integration 层

### integration/sharksocket/adapter.go

```go
// integration/sharksocket/adapter.go

// Package sharksocket 提供 shark-mqtt 与 shark-socket Gateway 的集成适配器
//
// 使用方式：
//
//	import (
//	    ss "github.com/yourorg/shark-socket/api"
//	    mqttadapter "github.com/yourorg/shark-mqtt/integration/sharksocket"
//	)
//
//	gw := ss.NewGateway().
//	    AddServer(mqttadapter.New(":1883", mqttOpts...)).
//	    Build()
package sharksocket

import (
    "context"

    "github.com/yourorg/shark-mqtt/server"
    // shark-socket-api 共享接口包
    ssapi "github.com/yourorg/shark-socket-api"
)

// MQTTAdapter 将 shark-mqtt MQTTServer 适配为 shark-socket ProtocolServer 接口
// 使 MQTT Broker 能够纳入 shark-socket Gateway 统一管理
type MQTTAdapter struct {
    srv *server.MQTTServer
}

// 编译期断言：MQTTAdapter 实现 ssapi.ProtocolServer
var _ ssapi.ProtocolServer = (*MQTTAdapter)(nil)

func New(opts ...server.Option) *MQTTAdapter {
    return &MQTTAdapter{
        srv: server.New(opts...),
    }
}

func (a *MQTTAdapter) Protocol() ssapi.ProtocolType {
    return ssapi.MQTT
}

func (a *MQTTAdapter) Start() error {
    return a.srv.Start()
}

func (a *MQTTAdapter) Stop(ctx context.Context) error {
    return a.srv.Stop(ctx)
}

func (a *MQTTAdapter) ActiveConnections() int64 {
    return a.srv.ActiveConnections()
}

// Broker 返回底层 Broker，用于高级场景（如跨协议消息路由）
func (a *MQTTAdapter) Broker() *broker.Broker {
    return a.srv.Broker()
}
```

---

## API 层

### api/api.go

```go
// api/api.go

// Package api 是 shark-mqtt 的统一公共入口
//
// 使用示例（独立运行）：
//
//	broker := api.NewBroker(
//	    api.WithAddr(":1883"),
//	    api.WithTLS(tlsCfg),
//	    api.WithAuth(myAuth),
//	    api.WithRedisStore(redisClient),
//	)
//	if err := broker.Start(); err != nil {
//	    log.Fatal(err)
//	}
package api

import (
    "github.com/yourorg/shark-mqtt/auth"
    "github.com/yourorg/shark-mqtt/broker"
    "github.com/yourorg/shark-mqtt/infra/logger"
    "github.com/yourorg/shark-mqtt/infra/metrics"
    "github.com/yourorg/shark-mqtt/plugin"
    "github.com/yourorg/shark-mqtt/server"
    "github.com/yourorg/shark-mqtt/session"
    "github.com/yourorg/shark-mqtt/store"
    memstore "github.com/yourorg/shark-mqtt/store/memory"
)

// ── 类型导出 ──────────────────────────────────────────────

type (
    // 服务器
    Broker  = server.MQTTServer
    Client  = client.MQTTClient

    // 会话
    Session     = session.MQTTSession
    CloseReason = session.CloseReason
    SessionStats = session.Stats

    // 存储
    SessionStore  = store.SessionStore
    MessageStore  = store.MessageStore
    RetainedStore = store.RetainedStore

    // 认证
    Authenticator = auth.Authenticator
    AuthRequest   = auth.Request

    // 插件
    Plugin     = plugin.MQTTPlugin
    BasePlugin = plugin.BaseMQTTPlugin

    // 基础设施
    Logger  = logger.Logger
    Metrics = metrics.Metrics
)

// ── 关闭原因常量导出 ──────────────────────────────────────

const (
    CloseNormal     = session.CloseReasonNormal
    CloseTimeout    = session.CloseReasonTimeout
    CloseError      = session.CloseReasonError
    CloseKicked     = session.CloseReasonKicked
    CloseServerShut = session.CloseReasonServerShut
    CloseAuthFailed = session.CloseReasonAuthFailed
)

// ── Broker 工厂方法 ────────────────────────────────────────

// NewBroker 创建并返回 MQTT Broker
// 默认使用内存存储，无外部依赖，开箱即用
func NewBroker(opts ...server.Option) *Broker {
    return server.New(opts...)
}

// ── 选项函数导出（透传，无需用户 import server 包）────────

var (
    WithAddr           = server.WithAddr
    WithTLS            = server.WithTLS
    WithTLSFromFiles   = server.WithTLSFromFiles
    WithConnectTimeout = server.WithConnectTimeout
    WithMaxPacketSize  = server.WithMaxPacketSize
    WithSessionStore   = server.WithSessionStore
    WithMessageStore   = server.WithMessageStore
    WithRetainedStore  = server.WithRetainedStore
    WithAuthenticator  = server.WithAuthenticator
    WithPlugin         = server.WithPlugin
    WithLogger         = server.WithLogger
    WithMetrics        = server.WithMetrics
)

// ── 存储工厂方法 ──────────────────────────────────────────

// NewMemoryStore 创建内存存储三件套（开箱即用）
func NewMemoryStore() (store.SessionStore, store.MessageStore, store.RetainedStore) {
    return memstore.NewSessionStore(),
        memstore.NewMessageStore(),
        memstore.NewRetainedStore()
}

// NewRedisStore 创建 Redis 存储（需要 import shark-mqtt/store/redis）
// 此函数在 api 包仅作声明，实际由 store/redis 包提供
// 使用方式：
//   import redisstore "github.com/yourorg/shark-mqtt/store/redis"
//   ss, ms, rs := redisstore.New(redisClient)

// ── 认证工厂方法 ──────────────────────────────────────────

// NewNoopAuth 全部放行的认证器（开发/测试用）
func NewNoopAuth() Authenticator {
    return auth.NewNoopAuthenticator()
}

// NewFuncAuth 函数式认证器
func NewFuncAuth(fn func(req *AuthRequest) bool) Authenticator {
    return auth.NewFuncAuthenticator(func(ctx context.Context, req *auth.Request) bool {
        return fn(req)
    })
}

// NewChainAuth 链式认证器（所有认证器都通过才放行）
func NewChainAuth(auths ...Authenticator) Authenticator {
    return auth.NewChainAuthenticator(auths...)
}

// ── 客户端工厂方法 ────────────────────────────────────────

// NewClient 创建 MQTT 客户端
func NewClient(addr string, opts ...client.Option) *Client {
    return client.New(addr, opts...)
}

// ── 基础设施工厂方法 ──────────────────────────────────────

// NewLogger 创建默认日志器（基于 slog）
func NewLogger(opts ...logger.Option) Logger {
    return logger.New(opts...)
}

// NewNoopMetrics 创建空指标采集器
func NewNoopMetrics() Metrics {
    return metrics.Default()
}
```

---

## 数据流设计

### 连接建立与持久会话恢复

```
TCP 连接到来
    │
    ▼
[server.acceptLoop]
    │ plugin.OnAccept(remoteAddr) == false → 关闭连接
    ▼
[server.handleConn]
    │ codec.DecodeConnect()  失败 → 关闭连接
    ▼
[broker.HandleConnect]
    ├── auth.Authenticate()  失败 → SendConnAck(BadCredentials) → 关闭
    │
    ├── registry.Connect()
    │     ├── 踢下线同 ClientID 的在线会话（Detach）
    │     ├── CleanSession=false 且有持久会话 → Attach(newConn)
    │     └── 否则 → NewMQTTSession + Attach(conn)
    │
    ├── SessionPresent=true → redeliverOfflineMessages
    └── deliverRetainedOnSubscribe（针对已有订阅）
    │
    ▼
codec.SendConnAck(Accepted, sessionPresent)
    │
    ▼
plugin.OnConnected(sess)
    │
    ▼
server.messageLoop(conn, sess)
```

### PUBLISH 消息路由

```
客户端发送 PUBLISH(topic, payload, QoS=1)
    │
    ▼
[codec.Decode] → PublishPacket
    │
    ▼
[plugin.OnMessage] → 可拦截
    │
    ▼
[broker.HandlePublish]
    │
    ├── Retain=true → retainedStore.Set(topic, msg)
    │
    ├── topics.Match(topic) → []MQTTSession（订阅者列表）
    │
    └── 对每个订阅者 deliverToSubscriber：
            │
            ├── IsOnline=false + CleanSession=false + QoS>0
            │     └── messageStore.Enqueue（离线消息）
            │
            ├── QoS0 → sess.SendPublish(msg, 0)
            │
            └── QoS1/2 → qos.Enqueue(sess, msg, qos)
                            │
                            ├── sess.SendPublish(msg, packetID)
                            └── 记录 inflight，启动重试计时
    │
    ▼
sess.SendPubAck(packetID)  ← 向发布者确认
```

### QoS 2 完整流程

```
Broker → PUBLISH(packetID=42, QoS=2) → Client
         inflight[clientID][42] = {state: SENT}

Client → PUBREC(packetID=42) → Broker
         qos.AckPubRec("clientA", 42)
         inflight[42].state = PUBREL_SENT
         Broker → PUBREL(packetID=42) → Client

Client → PUBCOMP(packetID=42) → Broker
         qos.AckPubComp("clientA", 42)
         delete(inflight[clientID][42])
         消息投递完成 ✓

重试路径（PUBREL 超时）：
  retryLoop 触发
  inflight[42].state == PUBREL_SENT
  → Broker → PUBREL(packetID=42, DUP=true) → Client
  retries++

超过 maxRetries：
  → sess.Close(CloseReasonTimeout)
  → HandleDisconnect → 触发遗嘱消息（如配置）
```

---

## 扩展指南

### 实现自定义存储

```go
// 以 Redis 存储为例
// store/redis/session_store.go

package redis

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "github.com/redis/go-redis/v9"
    "github.com/yourorg/shark-mqtt/store"
)

type RedisSessionStore struct {
    client *redis.Client
    ttl    time.Duration // 持久会话 TTL（0 表示永不过期）
}

func NewSessionStore(client *redis.Client, ttl time.Duration) store.SessionStore {
    return &RedisSessionStore{client: client, ttl: ttl}
}

// 编译期断言
var _ store.SessionStore = (*RedisSessionStore)(nil)

func (s *RedisSessionStore) Save(ctx context.Context, sess *store.PersistedSession) error {
    data, err := json.Marshal(sess)
    if err != nil {
        return fmt.Errorf("redis session store marshal: %w", err)
    }
    key := "mqtt:session:" + sess.ClientID
    if s.ttl > 0 {
        return s.client.Set(ctx, key, data, s.ttl).Err()
    }
    return s.client.Set(ctx, key, data, 0).Err()
}

func (s *RedisSessionStore) Load(
    ctx context.Context,
    clientID string,
) (*store.PersistedSession, bool, error) {
    key := "mqtt:session:" + clientID
    data, err := s.client.Get(ctx, key).Bytes()
    if err == redis.Nil {
        return nil, false, nil
    }
    if err != nil {
        return nil, false, fmt.Errorf("redis session store get: %w", err)
    }
    var sess store.PersistedSession
    if err := json.Unmarshal(data, &sess); err != nil {
        return nil, false, fmt.Errorf("redis session store unmarshal: %w", err)
    }
    return &sess, true, nil
}

func (s *RedisSessionStore) Delete(ctx context.Context, clientID string) error {
    return s.client.Del(ctx, "mqtt:session:"+clientID).Err()
}
```

### 实现自定义插件

```go
// 以 ACL 访问控制插件为例

type ACLPlugin struct {
    plugin.BaseMQTTPlugin
    rules ACLRules
}

func (p *ACLPlugin) Name() string  { return "acl" }
func (p *ACLPlugin) Priority() int { return 10 } // 高优先级，早于其他插件执行

func (p *ACLPlugin) OnPublish(
    sess *session.MQTTSession,
    pkt *protocol.PublishPacket,
) bool {
    return p.rules.CanPublish(sess.ClientID(), pkt.Topic)
}

func (p *ACLPlugin) OnSubscribe(
    sess *session.MQTTSession,
    filter string,
    _ protocol.QoSLevel,
) bool {
    return p.rules.CanSubscribe(sess.ClientID(), filter)
}
```

---

## 测试策略

### 分层测试

```
单元测试（无网络）：
  broker/broker_test.go      → 使用 mock session 测试路由逻辑
  broker/topic_tree_test.go  → 测试通配符匹配所有边界情况
  broker/qos_engine_test.go  → 测试状态机转换和超时重试
  session/session_test.go    → 测试 Attach/Detach/Close 状态机
  session/registry_test.go   → 测试持久会话恢复和踢下线
  protocol/codec_test.go     → 测试编解码的所有报文类型

集成测试（真实连接）：
  test/integration/connect_test.go           → CONNECT/CONNACK 流程
  test/integration/pubsub_test.go            → PUBLISH/SUBSCRIBE 端到端
  test/integration/qos_test.go               → QoS 0/1/2 完整流程
  test/integration/persistent_session_test.go → 断线重连持久会话
  test/integration/will_test.go              → 遗嘱消息触发

基准测试：
  test/bench/throughput_test.go  → 消息吞吐量（msg/s）
  test/bench/latency_test.go     → 端到端延迟（P50/P95/P99）
```

### 编译期断言清单

```go
// 在每个实现文件中包含，确保接口完整实现

// store/redis/session_store.go
var _ store.SessionStore  = (*RedisSessionStore)(nil)
var _ store.MessageStore  = (*RedisMessageStore)(nil)
var _ store.RetainedStore = (*RedisRetainedStore)(nil)

// auth/file.go
var _ auth.Authenticator = (*FileAuthenticator)(nil)

// plugin/acl.go
var _ plugin.MQTTPlugin = (*ACLPlugin)(nil)

// integration/sharksocket/adapter.go
var _ ssapi.ProtocolServer = (*MQTTAdapter)(nil)
```

### 测试命令

```bash
# 所有测试（含竞态检测）
go test -race ./...

# 仅单元测试
go test -race ./broker/... ./session/... ./protocol/...

# 集成测试
go test -race -tags=integration ./test/integration/...

# 基准测试
go test -bench=. -benchmem -benchtime=10s ./test/bench/...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

---

## 设计决策记录

### ADR-001：独立仓库而非子包

**决策**：shark-mqtt 作为独立 Go module（`github.com/yourorg/shark-mqtt`）

**原因**：
- MQTT 代码量（8000~20000 行）与其他协议不在一个量级
- 可选外部依赖（Redis/BadgerDB）不应污染主框架
- 支持独立发版、独立部署、独立扩缩容
- 与 shark-socket 的集成通过 `integration/sharksocket` 适配器完成

**代价**：接口变更需要同步更新 shark-socket-api 共享包

---

### ADR-002：网络层与业务层分离（MQTTServer vs Broker）

**决策**：`server.MQTTServer` 只负责网络，`broker.Broker` 只负责业务

**原因**：
- Broker 可以在无网络环境下进行纯单元测试
- 网络层可以独立替换（WebSocket over MQTT、QUIC over MQTT 等）
- 职责清晰，每个类型只有一个变化原因

---

### ADR-003：会话连接分离（Attach/Detach 模型）

**决策**：`MQTTSession.Attach(conn)` / `MQTTSession.Detach()` 而非重建会话

**原因**：
- MQTT 持久会话的核心语义：会话存活时间 > 连接存活时间
- Attach/Detach 使连接热替换对上层透明
- 避免重连时重新初始化订阅列表和 QoS 状态

---

### ADR-004：writeLoop 随 Attach 启动，随 Detach 退出

**决策**：每次 `Attach` 启动一个新的写协程，读 `writeChan` 直到 channel 关闭

**原因**：
- 写协程生命周期 = 连接生命周期（而非会话生命周期）
- `Detach` 通过 `close(writeChan)` 优雅终止写协程
- 避免了锁竞争（写协程是 channel 的唯一消费者）

---

### ADR-005：TopicTree 保留系统主题不被普通通配符匹配

**决策**：`$` 开头的主题不被 `+` 和 `#` 匹配（符合 MQTT 规范 §4.7.2）

**原因**：
- MQTT 规范明确要求：`$SYS/#` 不被 `#` 订阅匹配
- 防止普通订阅者意外收到 Broker 内部系统消息