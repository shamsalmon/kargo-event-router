package sink

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/shamsalmon/kargo-event-router/pkg/payload"
)

const (
	// SignatureHeader is the header in which the webhook sink sends the
	// HMAC-SHA256 signature of the request body when a signing key is
	// configured.
	SignatureHeader = "X-Kargo-Event-Router-Signature"

	contentTypeCloudEvents = "application/cloudevents+json"

	maxResponseBytes = 4096
)

type webhookSink struct {
	url        string
	signingKey []byte
	httpClient *http.Client
}

// newWebhookSink returns a Sink that POSTs events as CloudEvents to the
// given URL. When signingKey is non-empty, each request body is signed with
// HMAC-SHA256 and the signature is sent in the
// X-Kargo-Event-Router-Signature header.
func newWebhookSink(url string, signingKey []byte, timeout time.Duration) Sink {
	return &webhookSink{
		url:        url,
		signingKey: signingKey,
		httpClient: newHTTPClient(timeout),
	}
}

// Send delivers the structured event; the pre-rendered text is ignored, as
// webhook consumers receive the full event and can render their own
// messages.
func (w *webhookSink) Send(
	ctx context.Context,
	evt *payload.CloudEvent,
	_ string,
) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("error marshaling event: %w", err)
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		w.url,
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("error building request for %q: %w", w.url, err)
	}
	req.Header.Set("Content-Type", contentTypeCloudEvents)
	if len(w.signingKey) > 0 {
		mac := hmac.New(sha256.New, w.signingKey)
		_, _ = mac.Write(body) // never returns an error
		req.Header.Set(
			SignatureHeader,
			fmt.Sprintf("sha256=%s", hex.EncodeToString(mac.Sum(nil))),
		)
	}
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request to %q: %w", w.url, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < http.StatusOK ||
		resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf(
			"received unexpected status code %d from %q", resp.StatusCode, w.url,
		)
	}
	return nil
}
