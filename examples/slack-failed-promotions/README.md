# Slack messages on failed promotions

These manifests post a Slack message whenever a Promotion fails in the
`kargo-demo` Project. Adjust namespaces, channel names, and Stage names to
taste, then apply with:

```bash
kubectl apply -f <file>.yaml
```

| File | What it does |
|---|---|
| [`incoming-webhook.yaml`](incoming-webhook.yaml) | Simplest setup: post to a single Slack channel via an [incoming webhook](https://api.slack.com/messaging/webhooks). |
| [`bot-token.yaml`](bot-token.yaml) | Post via a Slack app bot token (`chat:write` scope). One token serves many channels; each `MessageChannel` picks its own channel. |
| [`prod-only-with-expression.yaml`](prod-only-with-expression.yaml) | Only alert on failures in the `production` Stage, using a `when` expression, and include verification (AnalysisRun) failures. |

All failed-promotion routing keys off two event types:

- `PromotionFailed` — the promotion process ran and failed (e.g. a step
  failed)
- `PromotionErrored` — the promotion hit an internal error

If you also want to know when post-promotion verification fails (a failed
`AnalysisRun` spawned by the Stage), add `FreightVerificationFailed` and
`FreightVerificationErrored`, as the third example does.
