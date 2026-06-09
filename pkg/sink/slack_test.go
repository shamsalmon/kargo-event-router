package sink

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/shamsalmon/kargo-event-router/pkg/payload"
)

func newTestCloudEvent() *payload.CloudEvent {
	return &payload.CloudEvent{
		SpecVersion: "1.0",
		ID:          "test-uid",
		Source:      "kargo/kargo-demo",
		Type:        "io.akuity.kargo.promotion-failed",
		Subject:     "Promotion/test-promotion",
		Stage:       "prod",
		Data: map[string]any{
			"message": "something broke",
		},
	}
}

func TestSlackWebhookSinkSend(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		handler http.HandlerFunc
		assert  func(*testing.T, error)
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				message := map[string]string{}
				require.NoError(t, json.Unmarshal(body, &message))
				require.Contains(t, message["text"], ":x: *Promotion Failed*")
				require.Contains(t, message["text"], "`kargo-demo`")
				require.Contains(t, message["text"], "`prod`")
				require.Contains(t, message["text"], "> something broke")
				// Incoming webhooks respond with a plain-text body.
				_, _ = w.Write([]byte("ok"))
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "non-2xx response",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "no_service", http.StatusNotFound)
			},
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "404")
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(testCase.handler)
			t.Cleanup(srv.Close)
			s := newSlackWebhookSink(srv.URL, 5*time.Second)
			testCase.assert(t, s.Send(context.Background(), newTestCloudEvent()))
		})
	}
}

func TestSlackAPISinkSend(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		handler http.HandlerFunc
		assert  func(*testing.T, error)
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				message := map[string]string{}
				require.NoError(t, json.Unmarshal(body, &message))
				require.Equal(t, "#deployments", message["channel"])
				require.NotEmpty(t, message["text"])
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "API-level error with 200 status",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":false,"error":"channel_not_found"}`))
			},
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "channel_not_found")
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(testCase.handler)
			t.Cleanup(srv.Close)
			s := &slackSink{
				token:      "test-token",
				channel:    "#deployments",
				apiURL:     srv.URL,
				httpClient: newHTTPClient(5 * time.Second),
			}
			testCase.assert(t, s.Send(context.Background(), newTestCloudEvent()))
		})
	}
}

func TestMessageText(t *testing.T) {
	t.Parallel()
	text := messageText(newTestCloudEvent())
	require.Equal(
		t,
		":x: *Promotion Failed*\n"+
			"*Project:* `kargo-demo`\n"+
			"*Stage:* `prod`\n"+
			"*Resource:* `Promotion/test-promotion`\n"+
			"> something broke",
		text,
	)
}

func TestHumanizeEventType(t *testing.T) {
	t.Parallel()
	testCases := map[string]string{
		"io.akuity.kargo.promotion-failed":               "Promotion Failed",
		"io.akuity.kargo.freight-verification-succeeded": "Freight Verification Succeeded",
		"promotion-aborted":                              "Promotion Aborted",
	}
	for in, want := range testCases {
		require.Equal(t, want, humanizeEventType(in))
	}
}
