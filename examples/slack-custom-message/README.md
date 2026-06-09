# Custom Slack messages with output templates

[`promotion-started.yaml`](promotion-started.yaml) posts a custom message to
`#deployments` whenever a promotion kicks off, instead of the default
rendered summary:

> Kargo has kicked off promotion to stage: production.

The `output` field on a channel reference is a template in which `${{ }}`
blocks contain [expr-lang](https://expr-lang.org) expressions evaluated
against the same `event` object available to `when` expressions —
`event.stageName`, `event.promotionName`, `event.freightAlias`,
`event.message`, and so on. Full expressions work too:

```yaml
output: "Freight ${{ event.freightAlias }} is being promoted to ${{ event.stageName }}."
```

`output` is per channel reference, so the same router can deliver a custom
message to one channel and the default rendering to another. Webhook
channels always receive the full structured event and ignore `output`.

Apply with:

```bash
kubectl apply -f promotion-started.yaml
```
