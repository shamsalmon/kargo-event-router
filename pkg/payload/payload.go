// Package payload converts Kargo events stored as Kubernetes Events into
// CloudEvents 1.0 envelopes suitable for delivery to external systems.
package payload

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	corev1 "k8s.io/api/core/v1"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	libevent "github.com/akuity/kargo/pkg/event"
)

// eventTypePrefix is the reverse-DNS prefix used for the CloudEvents type
// attribute.
const eventTypePrefix = "io.akuity.kargo."

// CloudEvent is a minimal representation of a CloudEvents 1.0 envelope in
// structured JSON mode. See https://cloudevents.io for the specification.
type CloudEvent struct {
	SpecVersion     string    `json:"specversion"`
	ID              string    `json:"id"`
	Source          string    `json:"source"`
	Type            string    `json:"type"`
	Subject         string    `json:"subject,omitempty"`
	Time            time.Time `json:"time"`
	DataContentType string    `json:"datacontenttype"`
	Data            any       `json:"data"`
}

// New converts the given Kubernetes Event, which must have been recorded by
// Kargo, into a CloudEvent. The event's reason identifies the Kargo event
// type and its annotations carry the structured event data.
func New(evt *corev1.Event) (*CloudEvent, error) {
	data, err := dataFromEvent(evt)
	if err != nil {
		return nil, fmt.Errorf(
			"error unmarshaling data for event of type %q: %w", evt.Reason, err,
		)
	}
	return &CloudEvent{
		SpecVersion: "1.0",
		ID:          string(evt.UID),
		Source:      fmt.Sprintf("kargo/%s", evt.Namespace),
		Type:        eventTypePrefix + kebabCase(evt.Reason),
		Subject: fmt.Sprintf(
			"%s/%s", evt.InvolvedObject.Kind, evt.InvolvedObject.Name,
		),
		Time:            eventTime(evt),
		DataContentType: "application/json",
		Data:            data,
	}, nil
}

// dataFromEvent reconstructs the typed Kargo event from the Kubernetes
// Event's annotations. Unrecognized event types degrade gracefully to a
// generic representation of the message and annotations.
func dataFromEvent(evt *corev1.Event) (any, error) {
	id := string(evt.UID)
	ann := evt.Annotations
	var (
		data any
		err  error
	)
	switch kargoapi.EventType(evt.Reason) {
	case kargoapi.EventTypePromotionCreated:
		data, err = libevent.UnmarshalPromotionCreatedAnnotations(id, ann)
	case kargoapi.EventTypePromotionSucceeded:
		data, err = libevent.UnmarshalPromotionSucceededAnnotations(id, ann)
	case kargoapi.EventTypePromotionFailed:
		data, err = libevent.UnmarshalPromotionFailedAnnotations(id, ann)
	case kargoapi.EventTypePromotionErrored:
		data, err = libevent.UnmarshalPromotionErroredAnnotations(id, ann)
	case kargoapi.EventTypePromotionAborted:
		data, err = libevent.UnmarshalPromotionAbortedAnnotations(id, ann)
	case kargoapi.EventTypeFreightApproved:
		data, err = libevent.UnmarshalFreightApprovedAnnotations(id, ann)
	case kargoapi.EventTypeFreightVerificationSucceeded:
		data, err = libevent.UnmarshalFreightVerificationSucceededAnnotations(id, ann)
	case kargoapi.EventTypeFreightVerificationFailed:
		data, err = libevent.UnmarshalFreightVerificationFailedAnnotations(id, ann)
	case kargoapi.EventTypeFreightVerificationErrored:
		data, err = libevent.UnmarshalFreightVerificationErroredAnnotations(id, ann)
	case kargoapi.EventTypeFreightVerificationAborted:
		data, err = libevent.UnmarshalFreightVerificationAbortedAnnotations(id, ann)
	case kargoapi.EventTypeFreightVerificationInconclusive:
		data, err = libevent.UnmarshalFreightVerificationInconclusiveAnnotations(id, ann)
	case kargoapi.EventTypeFreightVerificationUnknown:
		data, err = libevent.UnmarshalFreightVerificationUnknownAnnotations(id, ann)
	default:
		return genericData(evt), nil
	}
	if err != nil {
		return nil, err
	}
	// The message is stored on the Kubernetes Event itself rather than in
	// annotations, so it must be restored separately.
	if m, ok := data.(libevent.Message); ok {
		m.SetMessage(evt.Message)
	}
	return data, nil
}

// genericData builds a fallback representation for event types this package
// does not recognize, preserving the message and any Kargo event annotations.
func genericData(evt *corev1.Event) map[string]any {
	annotations := map[string]string{}
	for k, v := range evt.Annotations {
		if strings.HasPrefix(k, kargoapi.AnnotationKeyEventPrefix) {
			annotations[k] = v
		}
	}
	return map[string]any{
		"message":     evt.Message,
		"annotations": annotations,
	}
}

// eventTime returns the most meaningful timestamp available on the given
// Kubernetes Event.
func eventTime(evt *corev1.Event) time.Time {
	if !evt.LastTimestamp.IsZero() {
		return evt.LastTimestamp.Time
	}
	return evt.CreationTimestamp.Time
}

// kebabCase converts a PascalCase event type like "PromotionFailed" to
// "promotion-failed".
func kebabCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('-')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
