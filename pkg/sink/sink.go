// Package sink provides destinations to which events can be delivered.
package sink

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	kargonet "github.com/akuity/kargo/pkg/net"

	"github.com/shamsalmon/kargo-event-router/api/v1alpha1"
	"github.com/shamsalmon/kargo-event-router/pkg/payload"
)

// Keys expected in the data maps of Secrets referenced by MessageChannels.
const (
	// SecretKeySigningKey holds the HMAC signing key for webhook channels.
	SecretKeySigningKey = "secret"
	// SecretKeySlackToken holds a Slack bot token.
	SecretKeySlackToken = "token"
)

// Sink delivers an event to an external destination.
type Sink interface {
	// Send delivers the given event, returning an error if delivery fails.
	// text is a pre-rendered message for sinks that deliver human-readable
	// messages; when empty, such sinks fall back to their default rendering.
	// Sinks that deliver the structured event ignore it.
	Send(ctx context.Context, evt *payload.CloudEvent, text string) error
}

// New returns the Sink described by the given MessageChannel. secretData is
// the data map of the channel's referenced Secret, or nil if the channel
// references none.
func New(
	channel *v1alpha1.MessageChannel,
	secretData map[string][]byte,
	timeout time.Duration,
) (Sink, error) {
	switch {
	case channel.Spec.Webhook != nil:
		cfg := channel.Spec.Webhook
		var signingKey []byte
		if cfg.SecretRef != nil {
			key, ok := secretData[SecretKeySigningKey]
			if !ok {
				return nil, fmt.Errorf(
					"Secret %q has no %q key",
					cfg.SecretRef.Name, SecretKeySigningKey,
				)
			}
			signingKey = key
		}
		return newWebhookSink(cfg.URL, signingKey, timeout), nil
	case channel.Spec.Slack != nil:
		cfg := channel.Spec.Slack
		token, ok := secretData[SecretKeySlackToken]
		if !ok {
			return nil, fmt.Errorf(
				"Secret %q has no %q key",
				cfg.SecretRef.Name, SecretKeySlackToken,
			)
		}
		return newSlackSink(string(token), cfg.Channel, timeout), nil
	}
	return nil, errors.New("MessageChannel has no destination configured")
}

// newHTTPClient returns an http.Client with the given timeout whose
// transport refuses connections to link-local addresses to mitigate SSRF
// against cloud instance metadata endpoints.
func newHTTPClient(timeout time.Duration) *http.Client {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		transport = &http.Transport{}
	} else {
		transport = transport.Clone()
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: kargonet.SafeTransport(transport),
	}
}
