package dispatch

import (
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
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
// result="error" with the corresponding channel_type. The stage label is the
// Kargo Stage the event relates to, or empty for events that carry no stage.
var deliveriesTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "kargo_event_router_deliveries_total",
		Help: "Total number of event delivery attempts, by project, stage, " +
			"channel, channel type, event type, and result.",
	},
	[]string{"project", "stage", "channel", "channel_type", "event_type", "result"},
)

// eventsTotal counts Kargo events the router handles, independent of how many
// channels they are delivered to. Unlike deliveriesTotal it carries no channel
// or result dimension: it is incremented exactly once per event, by project,
// stage, and event type.
var eventsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "kargo_event_router_events_total",
		Help: "Total number of Kargo events handled, by project, stage, and " +
			"event type. Counted once per event, regardless of channels.",
	},
	[]string{"project", "stage", "event_type"},
)

// allEventTypes is the set of Kargo event types the events counter is
// pre-initialized for, so a 0 series exists for every project/stage/event type
// before any event arrives.
var allEventTypes = []kargoapi.EventType{
	kargoapi.EventTypePromotionCreated,
	kargoapi.EventTypePromotionSucceeded,
	kargoapi.EventTypePromotionFailed,
	kargoapi.EventTypePromotionErrored,
	kargoapi.EventTypePromotionAborted,
	kargoapi.EventTypeFreightApproved,
	kargoapi.EventTypeFreightVerificationSucceeded,
	kargoapi.EventTypeFreightVerificationFailed,
	kargoapi.EventTypeFreightVerificationErrored,
	kargoapi.EventTypeFreightVerificationAborted,
	kargoapi.EventTypeFreightVerificationInconclusive,
	kargoapi.EventTypeFreightVerificationUnknown,
}

func init() {
	// Registering with controller-runtime's registry exposes the metrics on
	// the manager's metrics endpoint alongside the built-in controller
	// metrics.
	ctrlmetrics.Registry.MustRegister(deliveriesTotal, eventsTotal)
}

// recordEvent increments the events counter for the given event, by project
// (the event's namespace), stage (from the event's annotations), and event
// type (the event's reason).
func recordEvent(evt *corev1.Event) {
	eventsTotal.WithLabelValues(
		evt.Namespace,
		evt.Annotations[kargoapi.AnnotationKeyEventStageName],
		evt.Reason,
	).Inc()
}

// initEventMetrics creates the events counter at 0 for the given project and
// stage across every known event type. Referencing a child series with
// WithLabelValues instantiates it at 0 without incrementing, so the series
// exists in /metrics before any event arrives. This is idempotent: it never
// resets an already-incremented counter.
func initEventMetrics(project, stage string) {
	for _, eventType := range allEventTypes {
		eventsTotal.WithLabelValues(project, stage, string(eventType))
	}
}
