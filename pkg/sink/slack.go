package sink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shamsalmon/kargo-event-router/pkg/payload"
)

const slackPostMessageURL = "https://slack.com/api/chat.postMessage"

type slackSink struct {
	// webhookURL is set in incoming-webhook mode.
	webhookURL string
	// token, channel, and apiURL are set in bot-token (chat.postMessage)
	// mode.
	token      string
	channel    string
	apiURL     string
	httpClient *http.Client
}

// newSlackWebhookSink returns a Sink that posts messages to a Slack incoming
// webhook.
func newSlackWebhookSink(webhookURL string, timeout time.Duration) Sink {
	return &slackSink{
		webhookURL: webhookURL,
		httpClient: newHTTPClient(timeout),
	}
}

// newSlackAPISink returns a Sink that posts messages to a Slack channel
// using the chat.postMessage API.
func newSlackAPISink(token, channel string, timeout time.Duration) Sink {
	return &slackSink{
		token:      token,
		channel:    channel,
		apiURL:     slackPostMessageURL,
		httpClient: newHTTPClient(timeout),
	}
}

func (s *slackSink) Send(ctx context.Context, evt *payload.CloudEvent) error {
	if s.webhookURL != "" {
		return s.post(
			ctx,
			s.webhookURL,
			"",
			map[string]string{"text": messageText(evt)},
		)
	}
	return s.post(
		ctx,
		s.apiURL,
		s.token,
		map[string]string{
			"channel": s.channel,
			"text":    messageText(evt),
		},
	)
}

// post sends the given message to the given Slack endpoint and interprets
// the response, which differs subtly between incoming webhooks (plain "ok")
// and Web API methods (JSON with an "ok" field).
func (s *slackSink) post(
	ctx context.Context,
	url string,
	token string,
	message map[string]string,
) error {
	body, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("error marshaling message: %w", err)
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		url,
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("error building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request to Slack: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < http.StatusOK ||
		resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf(
			"received unexpected status code %d from Slack: %s",
			resp.StatusCode, strings.TrimSpace(string(respBody)),
		)
	}
	// Web API methods return 200 even on failure, with ok=false and an
	// error code in the body.
	apiResp := struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}{}
	if err = json.Unmarshal(respBody, &apiResp); err != nil {
		// Incoming webhooks respond with plain "ok", which is not JSON.
		return nil
	}
	if !apiResp.OK {
		return fmt.Errorf("Slack API returned an error: %s", apiResp.Error)
	}
	return nil
}

// messageText renders the given event as Slack mrkdwn.
func messageText(evt *payload.CloudEvent) string {
	var b strings.Builder
	fmt.Fprintf(
		&b,
		"%s *%s*",
		messageEmoji(evt.Type),
		humanizeEventType(evt.Type),
	)
	fmt.Fprintf(
		&b,
		"\n*Project:* `%s`",
		strings.TrimPrefix(evt.Source, "kargo/"),
	)
	if evt.Stage != "" {
		fmt.Fprintf(&b, "\n*Stage:* `%s`", evt.Stage)
	}
	if evt.Subject != "" {
		fmt.Fprintf(&b, "\n*Resource:* `%s`", evt.Subject)
	}
	if message := dataMessage(evt.Data); message != "" {
		fmt.Fprintf(&b, "\n> %s", message)
	}
	return b.String()
}

// dataMessage extracts the human-readable message from an event's data.
func dataMessage(data any) string {
	if m, ok := data.(interface{ GetMessage() string }); ok {
		return m.GetMessage()
	}
	if m, ok := data.(map[string]any); ok {
		message, _ := m["message"].(string)
		return message
	}
	return ""
}

// messageEmoji returns an emoji appropriate to the given CloudEvents type.
func messageEmoji(ceType string) string {
	switch {
	case strings.Contains(ceType, "failed"),
		strings.Contains(ceType, "errored"):
		return ":x:"
	case strings.Contains(ceType, "succeeded"),
		strings.Contains(ceType, "approved"):
		return ":white_check_mark:"
	case strings.Contains(ceType, "aborted"),
		strings.Contains(ceType, "inconclusive"),
		strings.Contains(ceType, "unknown"):
		return ":warning:"
	default:
		return ":information_source:"
	}
}

// humanizeEventType converts a CloudEvents type like
// "io.akuity.kargo.promotion-failed" to "Promotion Failed".
func humanizeEventType(ceType string) string {
	if i := strings.LastIndex(ceType, "."); i >= 0 {
		ceType = ceType[i+1:]
	}
	words := strings.Split(ceType, "-")
	for i, word := range words {
		if word != "" {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}
