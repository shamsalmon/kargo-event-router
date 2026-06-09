package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name=Age,type=date,JSONPath=`.metadata.creationTimestamp`

// EventRouter describes which Kargo events occurring within its namespace
// (i.e. within a single Kargo Project) should be delivered, and to which
// channels.
type EventRouter struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec describes which events to route and the channels to deliver them
	// to.
	Spec EventRouterSpec `json:"spec,omitempty"`
	// Status describes the current status of the EventRouter.
	Status EventRouterStatus `json:"status,omitempty"`
}

// EventRouterSpec describes which events to route and the channels to
// deliver them to.
type EventRouterSpec struct {
	// Types is a list of Kargo event types this router applies to, e.g.
	// PromotionFailed or FreightVerificationFailed. When empty, the router
	// applies to all event types.
	//
	// +optional
	Types []kargoapi.EventType `json:"types,omitempty"`
	// Channels references the channels to which matching events are
	// delivered.
	//
	// +kubebuilder:validation:MinItems=1
	Channels []ChannelReference `json:"channels"`
	// When is an optional expr-lang expression that must evaluate to true
	// for an event to be delivered. The expression is evaluated against an
	// `event` object exposing the event's type, project, message, and all of
	// its Kargo annotations as camelCase fields, e.g.:
	//
	//   event.stageName == 'production'
	//   event.type == 'PromotionFailed' && event.freightAlias != ''
	//
	// +optional
	When string `json:"when,omitempty"`
}

// ChannelReference is a reference to a channel resource in the same
// namespace as the EventRouter.
type ChannelReference struct {
	// Name is the name of the referenced channel resource.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Kind is the kind of the referenced channel resource. MessageChannel is
	// the only currently supported kind and may be omitted.
	//
	// +kubebuilder:validation:Enum=MessageChannel
	// +optional
	Kind string `json:"kind,omitempty"`
	// Output is an optional template for the message delivered to this
	// channel, overriding the default rendering. ${{ }} blocks contain
	// expr-lang expressions evaluated against the same `event` object as the
	// When field, e.g.:
	//
	//   Kargo has kicked off promotion to stage: ${{ event.stageName }}.
	//
	// Output applies to channels that deliver human-readable messages (e.g.
	// Slack); webhook channels always receive the full structured event and
	// ignore it.
	//
	// +optional
	Output string `json:"output,omitempty"`
}

// EventRouterStatus describes the current status of an EventRouter.
type EventRouterStatus struct {
	// Conditions contains the last observations of the EventRouter's current
	// state.
	//
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchMergeKey:"type" patchStrategy:"merge"`
}

// +kubebuilder:object:root=true

// EventRouterList is a list of EventRouter resources.
type EventRouterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EventRouter `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EventRouter{}, &EventRouterList{})
}
