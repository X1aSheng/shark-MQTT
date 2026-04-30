package metrics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

// prometheusMetrics implements Metrics using Prometheus.
type prometheusMetrics struct {
	connections       prometheus.Counter
	disconnections    prometheus.Counter
	rejections        *prometheus.CounterVec
	authFailures      prometheus.Counter
	messagesPublished *prometheus.CounterVec
	messagesDelivered *prometheus.CounterVec
	messagesDropped   *prometheus.CounterVec
	inflight          prometheus.Gauge
	inflightDropped   prometheus.Counter
	retries           prometheus.Counter
	onlineSessions    prometheus.Gauge
	offlineSessions   prometheus.Gauge
	retainedMsgs      prometheus.Gauge
	subscriptions     prometheus.Gauge
	errors            *prometheus.CounterVec
}

// registerOrReuse registers a collector, returning the existing one on conflict.
// This prevents panics when NewPrometheusMetrics is called multiple times
// (e.g., in tests or process restarts where the DefaultRegisterer is reused).
func registerOrReuse[T prometheus.Collector](reg prometheus.Registerer, c T) T {
	err := reg.Register(c)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			return are.ExistingCollector.(T)
		}
		panic(err)
	}
	return c
}

// NewPrometheusMetrics creates a new Prometheus-backed Metrics implementation.
// Registers metrics with the provided Registerer (use prometheus.DefaultRegisterer if nil).
// Safe for repeated calls — reuses already-registered collectors instead of panicking.
func NewPrometheusMetrics(reg prometheus.Registerer) Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	m := &prometheusMetrics{}

	m.connections = registerOrReuse(reg, prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "shark_mqtt",
		Name:      "connections_total",
		Help:      "Total number of connections established",
	}))

	m.disconnections = registerOrReuse(reg, prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "shark_mqtt",
		Name:      "disconnections_total",
		Help:      "Total number of disconnections",
	}))

	m.rejections = registerOrReuse(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "shark_mqtt",
		Name:      "rejections_total",
		Help:      "Total number of connection rejections",
	}, []string{"reason"}))

	m.authFailures = registerOrReuse(reg, prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "shark_mqtt",
		Name:      "auth_failures_total",
		Help:      "Total number of authentication failures",
	}))

	m.messagesPublished = registerOrReuse(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "shark_mqtt",
		Name:      "messages_published_total",
		Help:      "Total number of messages published",
	}, []string{"qos"}))

	m.messagesDelivered = registerOrReuse(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "shark_mqtt",
		Name:      "messages_delivered_total",
		Help:      "Total number of messages delivered",
	}, []string{"qos"}))

	m.messagesDropped = registerOrReuse(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "shark_mqtt",
		Name:      "messages_dropped_total",
		Help:      "Total number of messages dropped",
	}, []string{"reason"}))

	m.inflight = registerOrReuse(reg, prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "shark_mqtt",
		Name:      "inflight_messages",
		Help:      "Total number of inflight messages",
	}))

	m.inflightDropped = registerOrReuse(reg, prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "shark_mqtt",
		Name:      "inflight_dropped_total",
		Help:      "Total number of inflight messages dropped",
	}))

	m.retries = registerOrReuse(reg, prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "shark_mqtt",
		Name:      "retries_total",
		Help:      "Total number of message retries",
	}))

	m.onlineSessions = registerOrReuse(reg, prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "shark_mqtt",
		Name:      "online_sessions",
		Help:      "Number of online sessions",
	}))

	m.offlineSessions = registerOrReuse(reg, prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "shark_mqtt",
		Name:      "offline_sessions",
		Help:      "Number of offline sessions",
	}))

	m.retainedMsgs = registerOrReuse(reg, prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "shark_mqtt",
		Name:      "retained_messages",
		Help:      "Number of retained messages",
	}))

	m.subscriptions = registerOrReuse(reg, prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "shark_mqtt",
		Name:      "subscriptions",
		Help:      "Total number of active subscriptions",
	}))

	m.errors = registerOrReuse(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "shark_mqtt",
		Name:      "errors_total",
		Help:      "Total number of errors by component",
	}, []string{"component"}))

	return m
}

func (m *prometheusMetrics) IncConnections() { m.connections.Inc() }
func (m *prometheusMetrics) OnDisconnect()   { m.disconnections.Inc() }
func (m *prometheusMetrics) IncRejections(reason string) {
	m.rejections.WithLabelValues(reason).Inc()
}
func (m *prometheusMetrics) IncAuthFailures() { m.authFailures.Inc() }
func (m *prometheusMetrics) IncMessagesPublished(qos uint8) {
	m.messagesPublished.WithLabelValues(qosLabel(qos)).Inc()
}
func (m *prometheusMetrics) IncMessagesDelivered(qos uint8) {
	m.messagesDelivered.WithLabelValues(qosLabel(qos)).Inc()
}
func (m *prometheusMetrics) IncMessagesDropped(reason string) {
	m.messagesDropped.WithLabelValues(reason).Inc()
}
func (m *prometheusMetrics) IncInflight(_ string) {
	m.inflight.Inc()
}
func (m *prometheusMetrics) DecInflight(_ string) {
	m.inflight.Dec()
}
func (m *prometheusMetrics) DecInflightBatch(_ string, count int) {
	m.inflight.Sub(float64(count))
}
func (m *prometheusMetrics) IncInflightDropped(_ string) {
	m.inflightDropped.Inc()
}
func (m *prometheusMetrics) IncRetries(_ string) {
	m.retries.Inc()
}
func (m *prometheusMetrics) SetOnlineSessions(count int) {
	m.onlineSessions.Set(float64(count))
}
func (m *prometheusMetrics) SetOfflineSessions(count int) {
	m.offlineSessions.Set(float64(count))
}
func (m *prometheusMetrics) SetRetainedMessages(count int) {
	m.retainedMsgs.Set(float64(count))
}
func (m *prometheusMetrics) SetSubscriptions(count int) {
	m.subscriptions.Set(float64(count))
}
func (m *prometheusMetrics) IncErrors(component string) {
	m.errors.WithLabelValues(component).Inc()
}

// qosLabel returns a safe Prometheus label for a QoS value.
func qosLabel(qos uint8) string {
	if qos > 2 {
		return "unknown"
	}
	return strconv.FormatUint(uint64(qos), 10)
}
