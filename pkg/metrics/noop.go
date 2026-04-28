package metrics

// noopMetrics is a no-op implementation of Metrics.
type noopMetrics struct{}

func (n *noopMetrics) IncConnections()                        {}
func (n *noopMetrics) DecConnections()                        {}
func (n *noopMetrics) IncRejections(_ string)                 {}
func (n *noopMetrics) IncAuthFailures()                       {}
func (n *noopMetrics) IncMessagesPublished(_ uint8)   {}
func (n *noopMetrics) IncMessagesDelivered(_ uint8)   {}
func (n *noopMetrics) IncMessagesDropped(_ string)            {}
func (n *noopMetrics) IncInflight(_ string)                   {}
func (n *noopMetrics) DecInflight(_ string)                   {}
func (n *noopMetrics) DecInflightBatch(_ string, _ int)       {}
func (n *noopMetrics) IncInflightDropped(_ string)            {}
func (n *noopMetrics) IncRetries(_ string)                    {}
func (n *noopMetrics) SetOnlineSessions(_ int)                {}
func (n *noopMetrics) SetOfflineSessions(_ int)               {}
func (n *noopMetrics) SetRetainedMessages(_ int)              {}
func (n *noopMetrics) SetSubscriptions(_ int)                 {}
func (n *noopMetrics) IncErrors(_ string)                     {}
