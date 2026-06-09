package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="URL",type="string",JSONPath=".spec.webhook.url"
// +kubebuilder:printcolumn:name=Age,type=date,JSONPath=`.metadata.creationTimestamp`

// EventRoute describes a destination to which Kargo events occurring within
// the EventRoute's namespace (i.e. within a single Kargo Project) are
// delivered.
type EventRoute struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec describes which events to route and where to deliver them.
	Spec EventRouteSpec `json:"spec,omitempty"`
	// Status describes the current status of the EventRoute.
	Status EventRouteStatus `json:"status,omitempty"`
}

// EventRouteSpec describes which events to route and where to deliver them.
type EventRouteSpec struct {
	// EventTypes is a list of Kargo event types this route applies to, e.g.
	// PromotionFailed or FreightVerificationFailed. When empty, the route
	// applies to all event types.
	//
	// +optional
	EventTypes []kargoapi.EventType `json:"eventTypes,omitempty"`
	// Stages is a list of Stage names this route applies to. When non-empty,
	// only events related to one of the named Stages are delivered. When
	// empty, the route applies to events related to any Stage.
	//
	// +optional
	Stages []string `json:"stages,omitempty"`
	// Webhook describes the destination to which matching events are
	// delivered.
	//
	// +kubebuilder:validation:Required
	Webhook WebhookSinkConfig `json:"webhook"`
}

// WebhookSinkConfig describes a webhook endpoint to which events are delivered
// as CloudEvents.
type WebhookSinkConfig struct {
	// URL is the address to which matching events are POSTed.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`
	// SecretRef references a Secret in the same namespace as the EventRoute.
	// When specified, the Secret's data map must contain a `secret` key whose
	// value is used to compute an HMAC-SHA256 signature of each request body.
	// The signature is sent in the X-Kargo-Event-Router-Signature header so
	// receivers can verify the authenticity of the payload.
	//
	// +optional
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`
}

// EventRouteStatus describes the current status of an EventRoute.
type EventRouteStatus struct {
	// Conditions contains the last observations of the EventRoute's current
	// state.
	//
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchMergeKey:"type" patchStrategy:"merge"`
}

// +kubebuilder:object:root=true

// EventRouteList is a list of EventRoute resources.
type EventRouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EventRoute `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EventRoute{}, &EventRouteList{})
}
