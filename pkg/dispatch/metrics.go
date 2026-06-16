package dispatch

import (
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

// Values of the result label.
const (
	resultSuccess      = "success"
	resultError        = "error"
	resultFailure      = "failure"
	resultAborted      = "aborted"
	resultCreated      = "created"
	resultApproved     = "approved"
	resultInconclusive = "inconclusive"
	resultUnknown      = "unknown"
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

// promotionsTotal counts every Promotion event the router dispatches. The
// result label carries the Promotion's outcome (success, failure, error,
// aborted, created), derived from the Kargo event type.
var promotionsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "kargo_event_router_promotions_total",
		Help: "Total number of Promotion events dispatched, by project, " +
			"stage, and result.",
	},
	[]string{"project", "stage", "result"},
)

// freightsTotal counts every non-verification Freight event the router
// dispatches (currently Freight approvals). Freight verification events are
// tracked separately by verificationsTotal. The result label carries the
// event's outcome, derived from the Kargo event type.
var freightsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "kargo_event_router_freights_total",
		Help: "Total number of non-verification Freight events dispatched, " +
			"by project, stage, and result.",
	},
	[]string{"project", "stage", "result"},
)

// verificationsTotal counts every Freight verification event the router
// dispatches. The result label carries the verification's outcome (success,
// failure, error, aborted, inconclusive, unknown), derived from the Kargo
// event type.
var verificationsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "kargo_event_router_verifications_total",
		Help: "Total number of Freight verification events dispatched, by " +
			"project, stage, and result.",
	},
	[]string{"project", "stage", "result"},
)

// promotionResults maps each Promotion event type to the value of the result
// label of the promotions counter.
var promotionResults = map[kargoapi.EventType]string{
	kargoapi.EventTypePromotionCreated:   resultCreated,
	kargoapi.EventTypePromotionSucceeded: resultSuccess,
	kargoapi.EventTypePromotionFailed:    resultFailure,
	kargoapi.EventTypePromotionErrored:   resultError,
	kargoapi.EventTypePromotionAborted:   resultAborted,
}

// freightResults maps each non-verification Freight event type to the value
// of the result label of the freights counter.
var freightResults = map[kargoapi.EventType]string{
	kargoapi.EventTypeFreightApproved: resultApproved,
}

// verificationResults maps each Freight verification event type to the value
// of the result label of the verifications counter.
var verificationResults = map[kargoapi.EventType]string{
	kargoapi.EventTypeFreightVerificationSucceeded:    resultSuccess,
	kargoapi.EventTypeFreightVerificationFailed:       resultFailure,
	kargoapi.EventTypeFreightVerificationErrored:      resultError,
	kargoapi.EventTypeFreightVerificationAborted:      resultAborted,
	kargoapi.EventTypeFreightVerificationInconclusive: resultInconclusive,
	kargoapi.EventTypeFreightVerificationUnknown:      resultUnknown,
}

func init() {
	// Registering with controller-runtime's registry exposes the metrics on
	// the manager's metrics endpoint alongside the built-in controller
	// metrics.
	ctrlmetrics.Registry.MustRegister(
		deliveriesTotal,
		promotionsTotal,
		freightsTotal,
		verificationsTotal,
	)
}

// recordEventDispatched increments the promotions or freights counter for the
// given event. Events whose type is neither a Promotion nor a Freight event
// are ignored. project is the event's namespace and stage is taken from the
// event's annotations; result is derived from the event type.
func recordEventDispatched(evt *corev1.Event) {
	eventType := kargoapi.EventType(evt.Reason)
	project := evt.Namespace
	stage := evt.Annotations[kargoapi.AnnotationKeyEventStageName]
	if result, ok := promotionResults[eventType]; ok {
		promotionsTotal.WithLabelValues(project, stage, result).Inc()
		return
	}
	if result, ok := freightResults[eventType]; ok {
		freightsTotal.WithLabelValues(project, stage, result).Inc()
		return
	}
	if result, ok := verificationResults[eventType]; ok {
		verificationsTotal.WithLabelValues(project, stage, result).Inc()
	}
}
