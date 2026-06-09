package sink

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/shamsalmon/kargo-event-router/api/v1alpha1"
)

func TestNew(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		spec       v1alpha1.MessageChannelSpec
		secretData map[string][]byte
		assert     func(*testing.T, Sink, error)
	}{
		{
			name: "webhook without signing",
			spec: v1alpha1.MessageChannelSpec{
				Webhook: &v1alpha1.WebhookChannelConfig{
					URL: "https://hooks.example.com",
				},
			},
			assert: func(t *testing.T, s Sink, err error) {
				require.NoError(t, err)
				require.IsType(t, &webhookSink{}, s)
			},
		},
		{
			name: "webhook with a SecretRef but no secret key",
			spec: v1alpha1.MessageChannelSpec{
				Webhook: &v1alpha1.WebhookChannelConfig{
					URL:       "https://hooks.example.com",
					SecretRef: &corev1.LocalObjectReference{Name: "test-secret"},
				},
			},
			secretData: map[string][]byte{"wrong-key": []byte("x")},
			assert: func(t *testing.T, _ Sink, err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), `no "secret" key`)
			},
		},
		{
			name: "slack incoming webhook",
			spec: v1alpha1.MessageChannelSpec{
				Slack: &v1alpha1.SlackChannelConfig{
					SecretRef: corev1.LocalObjectReference{Name: "test-secret"},
				},
			},
			secretData: map[string][]byte{
				SecretKeySlackWebhookURL: []byte("https://hooks.slack.com/services/x"),
			},
			assert: func(t *testing.T, s Sink, err error) {
				require.NoError(t, err)
				slack, ok := s.(*slackSink)
				require.True(t, ok)
				require.NotEmpty(t, slack.webhookURL)
			},
		},
		{
			name: "slack bot token requires a channel",
			spec: v1alpha1.MessageChannelSpec{
				Slack: &v1alpha1.SlackChannelConfig{
					SecretRef: corev1.LocalObjectReference{Name: "test-secret"},
				},
			},
			secretData: map[string][]byte{
				SecretKeySlackToken: []byte("xoxb-test"),
			},
			assert: func(t *testing.T, _ Sink, err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "channel is required")
			},
		},
		{
			name: "slack bot token with a channel",
			spec: v1alpha1.MessageChannelSpec{
				Slack: &v1alpha1.SlackChannelConfig{
					SecretRef: corev1.LocalObjectReference{Name: "test-secret"},
					Channel:   "#deployments",
				},
			},
			secretData: map[string][]byte{
				SecretKeySlackToken: []byte("xoxb-test"),
			},
			assert: func(t *testing.T, s Sink, err error) {
				require.NoError(t, err)
				slack, ok := s.(*slackSink)
				require.True(t, ok)
				require.Equal(t, "#deployments", slack.channel)
			},
		},
		{
			name: "slack Secret with neither key",
			spec: v1alpha1.MessageChannelSpec{
				Slack: &v1alpha1.SlackChannelConfig{
					SecretRef: corev1.LocalObjectReference{Name: "test-secret"},
				},
			},
			secretData: map[string][]byte{"wrong-key": []byte("x")},
			assert: func(t *testing.T, _ Sink, err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "neither")
			},
		},
		{
			name: "no destination configured",
			spec: v1alpha1.MessageChannelSpec{},
			assert: func(t *testing.T, _ Sink, err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "no destination")
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			s, err := New(
				&v1alpha1.MessageChannel{Spec: testCase.spec},
				testCase.secretData,
				5*time.Second,
			)
			testCase.assert(t, s, err)
		})
	}
}
