package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name=Age,type=date,JSONPath=`.metadata.creationTimestamp`

// MessageChannel describes a destination to which events can be delivered.
// EventRouters reference MessageChannels by name.
type MessageChannel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec describes the destination.
	Spec MessageChannelSpec `json:"spec,omitempty"`
}

// MessageChannelSpec describes a destination to which events can be
// delivered.
//
// +kubebuilder:validation:XValidation:message="exactly one of webhook or slack must be set",rule="has(self.webhook) != has(self.slack)"
type MessageChannelSpec struct {
	// Webhook describes a webhook destination to which events are delivered
	// as CloudEvents. Exactly one of Webhook or Slack must be set.
	//
	// +optional
	Webhook *WebhookChannelConfig `json:"webhook,omitempty"`
	// Slack describes a Slack destination to which events are delivered as
	// messages. Exactly one of Webhook or Slack must be set.
	//
	// +optional
	Slack *SlackChannelConfig `json:"slack,omitempty"`
}

// WebhookChannelConfig describes a webhook endpoint to which events are
// delivered as CloudEvents.
type WebhookChannelConfig struct {
	// URL is the address to which events are POSTed.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`
	// SecretRef references a Secret in the same namespace as the
	// MessageChannel. When specified, the Secret's data map must contain a
	// `secret` key whose value is used to compute an HMAC-SHA256 signature
	// of each request body. The signature is sent in the
	// X-Kargo-Event-Router-Signature header so receivers can verify the
	// authenticity of the payload.
	//
	// +optional
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`
}

// SlackChannelConfig describes a Slack channel to which events are delivered
// as messages.
type SlackChannelConfig struct {
	// SecretRef references a Secret in the same namespace as the
	// MessageChannel. The Secret's data map must contain one of the
	// following keys:
	//
	//   - `webhook-url`: a Slack incoming webhook URL. Messages are posted
	//     directly to it and the Channel field is ignored, as the channel is
	//     bound to the webhook itself.
	//   - `token`: a Slack bot token with the `chat:write` scope. Messages
	//     are posted using the chat.postMessage API and the Channel field is
	//     required.
	//
	// +kubebuilder:validation:Required
	SecretRef corev1.LocalObjectReference `json:"secretRef"`
	// Channel is the channel ID or name (e.g. `#deployments`) to post to.
	// Required when the referenced Secret provides a `token`; ignored when
	// it provides a `webhook-url`.
	//
	// +optional
	Channel string `json:"channel,omitempty"`
}

// +kubebuilder:object:root=true

// MessageChannelList is a list of MessageChannel resources.
type MessageChannelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MessageChannel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MessageChannel{}, &MessageChannelList{})
}
