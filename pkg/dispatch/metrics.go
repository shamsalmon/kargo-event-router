package dispatch

import (
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Values of the result label of the deliveries counter.
const (
	resultSuccess = "success"
	resultError   = "error"
)

// Values of the channel_type label of the deliveries counter.
const (
	channelTypeWebhook = "webhook"
	channelTypeSlack   = "slack"
	channelTypeUnknown = "unknown"
)

// deliveriesTotal counts every delivery attempt. "Messages sent
// successfully" is result="success"; failed Slack messages or webhooks are
// result="error" with the corresponding channel_type.
var deliveriesTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "kargo_event_router_deliveries_total",
		Help: "Total number of event delivery attempts, by project, " +
			"channel, channel type, event type, and result.",
	},
	[]string{"project", "channel", "channel_type", "event_type", "result"},
)

func init() {
	// Registering with controller-runtime's registry exposes the metric on
	// the manager's metrics endpoint alongside the built-in controller
	// metrics.
	ctrlmetrics.Registry.MustRegister(deliveriesTotal)
}
