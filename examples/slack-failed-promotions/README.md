# Slack messages on failed promotions

These manifests post a Slack message whenever a Promotion fails in the
`kargo-demo` Project. Adjust namespaces, channel names, and Stage names to
taste, then apply with:

```bash
kubectl apply -f <file>.yaml
```

Prerequisite for both examples: create a Slack app with the `chat:write`
scope, install it to your workspace, invite the bot to the target channels,
and paste its bot token (`xoxb-...`) into the Secret. Messages are posted
with the `chat.postMessage` API, so one token serves any number of channels.

| File | What it does |
|---|---|
| [`bot-token.yaml`](bot-token.yaml) | Post every failed promotion to `#deployments`. Defines two channels off one token. |
| [`prod-only-with-expression.yaml`](prod-only-with-expression.yaml) | Additionally page `#oncall`, but only for failures in the `production` Stage, using a `when` expression, and include verification (AnalysisRun) failures. |

All failed-promotion routing keys off two event types:

- `PromotionFailed` — the promotion process ran and failed (e.g. a step
  failed)
- `PromotionErrored` — the promotion hit an internal error

If you also want to know when post-promotion verification fails (a failed
`AnalysisRun` spawned by the Stage), add `FreightVerificationFailed` and
`FreightVerificationErrored`, as the second example does.
