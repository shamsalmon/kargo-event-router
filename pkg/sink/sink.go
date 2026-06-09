// Package sink provides destinations to which serialized event payloads can
// be delivered.
package sink

import (
	"context"
)

// Sink delivers a serialized event payload to an external destination.
type Sink interface {
	// Send delivers the given payload, returning an error if delivery fails.
	Send(ctx context.Context, payload []byte) error
}
