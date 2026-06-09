package sink

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	kargonet "github.com/akuity/kargo/pkg/net"
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

// NewWebhookSink returns a Sink that POSTs payloads to the given URL. When
// signingKey is non-empty, each request body is signed with HMAC-SHA256 and
// the signature is sent in the X-Kargo-Event-Router-Signature header.
func NewWebhookSink(url string, signingKey []byte, timeout time.Duration) Sink {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		transport = &http.Transport{}
	} else {
		transport = transport.Clone()
	}
	return &webhookSink{
		url:        url,
		signingKey: signingKey,
		httpClient: &http.Client{
			Timeout: timeout,
			// Refuse connections to link-local addresses to mitigate SSRF
			// against cloud instance metadata endpoints.
			Transport: kargonet.SafeTransport(transport),
		},
	}
}

func (w *webhookSink) Send(ctx context.Context, payload []byte) error {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		w.url,
		bytes.NewReader(payload),
	)
	if err != nil {
		return fmt.Errorf("error building request for %q: %w", w.url, err)
	}
	req.Header.Set("Content-Type", contentTypeCloudEvents)
	if len(w.signingKey) > 0 {
		mac := hmac.New(sha256.New, w.signingKey)
		_, _ = mac.Write(payload) // never returns an error
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
