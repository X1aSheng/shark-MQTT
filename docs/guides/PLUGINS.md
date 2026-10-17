# Writing Plugins

A plugin extends the broker without changing it. Two independent mechanisms are
available, and the choice between them is the main design decision:

| | Lifecycle | Event bus |
|---|---|---|
| Answers | "what should the broker do for me?" | "what happened?" |
| Ordering | ordered, synchronous | unordered, concurrent |
| Frequency | once per transition | per occurrence |
| A slow implementation | bounded (30s per stage) | bounded (5s per event, default) |
| Use for | connection pools, file handles, bridges | metrics, audit logs, tracing |

**Merging them is the usual mistake.** A single mechanism that does both forces
every observer to be ordered and every owner to be fire-and-forget. Keep them
separate.

## Quick start

```go
type AuditPlugin struct {
    published atomic.Int64
}

func (a *AuditPlugin) Name() string { return "audit" }

// Hooks returns the legacy packet-path callbacks. Returning nil is fine --
// a plugin may implement only the lifecycle, or only subscribe to the bus.
func (a *AuditPlugin) Hooks() []plugin.Hook { return nil }
func (a *AuditPlugin) Execute(context.Context, plugin.Hook, *plugin.Context) error {
    return nil
}

func (a *AuditPlugin) Initialize(ctx context.Context) error { return nil }
func (a *AuditPlugin) Start(ctx context.Context) error      { return nil }
func (a *AuditPlugin) Stop(ctx context.Context) error       { return nil }
func (a *AuditPlugin) Uninstall(ctx context.Context) error  { return nil }

func main() {
    mgr := plugin.NewManager()
    audit := &AuditPlugin{}
    mgr.Register(audit)

    // Observe without touching the packet path.
    mgr.Bus().Subscribe(plugin.EventMessagePublished, plugin.SubscriberFunc{
        SubscriberName: "audit",
        Fn: func(_ context.Context, ev plugin.EventData) error {
            audit.published.Add(1)
            return nil
        },
    })

    broker := api.NewBroker(api.WithPluginManager(mgr))
    if err := broker.Start(); err != nil { log.Fatal(err) }
    // broker.Stop() tears the plugin down.
}
```

For a plugin that only needs one or two stages, `LifecycleFuncs` avoids declaring
a type per stage:

```go
mgr.Register(&plugin.LifecycleFuncs{
    PluginName:   "metrics-exporter",
    OnInitialize: func(context.Context) error { return exporter.Connect() },
    OnStop:       func(context.Context) error { return exporter.Close() },
})
```

## Lifecycle

```
Initialize --> Start --> Stop --> Uninstall
```

The `Manager` drives these; the plugin holds no state about its own progress.

- Each stage runs **at most once**, and only in this order.
- Startup order is **by plugin name** (deterministic). Teardown is the reverse,
  so a plugin built later is torn down first.
- Every stage is bounded (30s) and panics are recovered. A plugin that hangs
  cannot stall the broker from starting or stopping.
- **`Start` failing still runs `Uninstall`.** Shutdown can happen after a
  partially failed startup, and skipping `Uninstall` would leak whatever
  `Initialize` acquired. For the same reason, `Stop` and `Uninstall` must
  tolerate being called when they never ran.
- A `Stop -> Start` cycle re-runs `Initialize -> Start` without repeating
  `Uninstall`.

A failing plugin never aborts startup: the broker is healthy, and refusing to
serve because an extension is broken would turn a small fault into an outage.
Failures are logged and counted (`errors_total{component="plugin_start"}`).

`Manager.StageOf(name)` reports progress for a status endpoint.

## Event bus

```go
bus.Subscribe(plugin.EventClientConnected, mySubscriber)
err := bus.Publish(ctx, plugin.EventClientConnected, plugin.EventData{
    ClientID: "sensor-1",
    Username: "sensor",
})
```

Events: `BrokerStarted`, `BrokerStopped`, `ClientConnected`,
`ClientDisconnected`, `MessagePublished`, `MessageDelivered`, `Subscribed`,
`Unsubscribed`, `RetainedChanged`, plus `PluginInitialized`, `PluginStarted`,
`PluginStopped`, `PluginUninstalled`.

Guarantees, all covered by tests:

- **Isolation.** A subscriber that errors or panics is reported; the others
  still receive the event.
- **Bounded.** A subscriber that ignores its context is abandoned at the
  timeout (default 5s). The publish path loses at most one timeout window, and
  never waits indefinitely.
- **Observable.** `bus.Dropped()` and `bus.Dispatched()` expose delivery
  counts, so a saturated plugin shows up as a metric rather than as missing data.
- **Errors name the culprit.** The error reads `subscriber <name>: ...`.

`Publish` takes the event kind as its own argument rather than reading it from
`EventData`. That is deliberate: a call site that forgot to set the field would
look up subscribers for `""` and deliver nothing -- indistinguishable from
"nobody subscribed". As a parameter, the compiler enforces it.

The broker publishes `EventMessagePublished` only for PUBLISH packets, while
the legacy `OnMessage` hook still fires for every packet type. Subscribers of
the bus event do not have to filter.

## Legacy hooks

The original four hooks remain and are unaffected:

| Hook | When | `Context` fields set |
|---|---|---|
| `OnAccept` | connection accepted, before CONNECT | `RemoteAddr` |
| `OnConnected` | CONNECT accepted, before CONNACK | `ClientID`, `Username` |
| `OnMessage` | every decoded packet | `ClientID`, plus `Topic`/`Payload`/`QoS`/`Retain` for PUBLISH |
| `OnClose` | connection torn down | `ClientID` |

Events and hooks fire together at the connection and publish sites, so plugins
written against the old API keep working unchanged.

## Deploying a plugin

Plugins are configured through the environment; the broker reads every config
key from env as well as from the config file, so no chart support is needed
beyond passing variables through:

```yaml
plugins:
  - name: MQTT_AUDIT_PLUGIN        # environment variable name
    config: true                   # reads ConfigMap "mqtt-audit-plugin", key "config"
    env:
      - name: MQTT_AUDIT_SINK
        value: "tcp://audit.internal:9000"
```

When `config: true`, the ConfigMap name is the plugin name lowercased with
underscores replaced by dashes, because ConfigMap names must be RFC 1123
subdomains while environment variable names are conventionally uppercase.

For secrets, add them under a plugin entry or use `extraEnv`; do not put them
in `values.yaml`, which is usually in git.

## Checklist

- [ ] `Stop` and `Uninstall` tolerate being called without a prior `Start`.
- [ ] `Handle` returns promptly and respects `ctx`.
- [ ] Subscribers are safe for concurrent use (the bus may call from several
      goroutines).
- [ ] Nothing is written to the event payload from `Handle` -- it is shared.
- [ ] Failure paths log enough to identify the plugin (the bus adds the name,
      but your own logs should too).