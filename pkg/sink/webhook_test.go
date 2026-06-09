package sink

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWebhookSinkSend(t *testing.T) {
	t.Parallel()

	testPayload := []byte(`{"specversion":"1.0"}`)
	testSigningKey := []byte("test-signing-key")

	testCases := []struct {
		name       string
		signingKey []byte
		handler    http.HandlerFunc
		assert     func(*testing.T, error)
	}{
		{
			name: "success without signature",
			handler: func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.Equal(t, testPayload, body)
				require.Equal(
					t,
					contentTypeCloudEvents,
					r.Header.Get("Content-Type"),
				)
				require.Empty(t, r.Header.Get(SignatureHeader))
				w.WriteHeader(http.StatusOK)
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:       "success with signature",
			signingKey: testSigningKey,
			handler: func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				mac := hmac.New(sha256.New, testSigningKey)
				_, _ = mac.Write(body)
				require.Equal(
					t,
					fmt.Sprintf("sha256=%s", hex.EncodeToString(mac.Sum(nil))),
					r.Header.Get(SignatureHeader),
				)
				w.WriteHeader(http.StatusNoContent)
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "non-2xx response",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "500")
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(testCase.handler)
			t.Cleanup(srv.Close)
			s := NewWebhookSink(srv.URL, testCase.signingKey, 5*time.Second)
			testCase.assert(t, s.Send(context.Background(), testPayload))
		})
	}
}

func TestWebhookSinkSendUnreachable(t *testing.T) {
	t.Parallel()
	s := NewWebhookSink(
		"http://127.0.0.1:1", // nothing listens here
		nil,
		time.Second,
	)
	require.Error(t, s.Send(context.Background(), []byte("{}")))
}
