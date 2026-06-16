# kargo-event-router

A standalone Kubernetes controller that routes [Kargo](https://kargo.io)
events — promotion failures, verification (AnalysisRun) failures, freight
approvals, and more — to Slack channels and webhook endpoints.

Kargo records everything that happens during promotion and verification as
Kubernetes Events with rich, structured annotations. This controller watches
those Events and delivers them according to `EventRouter` resources, which
select events by type and an optional `when` expression and reference one or
more `MessageChannel` destinations. No changes to Kargo itself are required.

## How it works

```
Kargo controllers / API server          kargo-event-router
┌───────────────────────────┐           ┌──────────────────────────────────┐
│ record Kargo events as    │  (etcd)   │ watch corev1.Event               │
│ corev1.Event (unchanged)  │ ────────▶ │  ├ match EventRouters in the     │
└───────────────────────────┘           │  │  event's namespace            │
                                        │  ├ deliver to each referenced    │
                                        │  │  MessageChannel (Slack or     │
                                        │  │  CloudEvents webhook)         │
                                        │  └ mark the event as routed      │
                                        └──────────────────────────────────┘
```

- The watch is restricted server-side (field selector on
  `involvedObject.apiVersion`) so only Kargo-related Events are cached.
- Typed event payloads are reconstructed using Kargo's own
  `github.com/akuity/kargo/pkg/event` library, so the JSON you receive mirrors
  Kargo's event model exactly.
- Delivery is **at-least-once**. Each successful delivery is recorded on the
  Event in a `kargo-event-router.io/routed-to` annotation (as
  `<router>/<channel>` pairs), so retries (and controller restarts) never
  re-deliver to a channel that already succeeded. Webhook consumers can
  deduplicate using the CloudEvent `id`, which is the Event's UID.
- Failed deliveries are retried with exponential backoff by the controller's
  workqueue.

## Installation

With Helm (chart and image are published to GHCR on every release):

```bash
helm install kargo-event-router \
  oci://ghcr.io/shamsalmon/charts/kargo-event-router \
  --namespace kargo-event-router \
  --create-namespace
```

See [`charts/kargo-event-router/values.yaml`](charts/kargo-event-router/values.yaml)
for the available options. Or with plain manifests:

```bash
kubectl apply -f config/crd -f config/rbac -f config/manager
```

The deployment runs a single replica. If you scale it up, set
`leaderElection.enabled=true` (Helm) or `ENABLE_LEADER_ELECTION=true` to
avoid duplicate deliveries.

## Usage

Routing is described by two resources, both namespaced to a Project:

- **`MessageChannel`** — a destination (a Slack channel or a webhook
  endpoint), defined once and shared by any number of routers.
- **`EventRouter`** — a routing rule: which event `types`, an optional
  `when` expression, and the `channels` to deliver to.

For example, to post to Slack when verification (an Argo Rollouts
`AnalysisRun` spawned by a Stage) fails, or a Promotion fails outright, in
the production Stage:

```yaml
apiVersion: kargo-event-router.io/v1alpha1
kind: EventRouter
metadata:
  name: prod-failures
  namespace: kargo-demo
spec:
  types:
  - FreightVerificationFailed
  - FreightVerificationErrored
  - PromotionFailed
  - PromotionErrored
  channels:
  - name: devops-team-slack
    kind: MessageChannel
  when: "event.stageName == 'production'"
---
apiVersion: kargo-event-router.io/v1alpha1
kind: MessageChannel
metadata:
  name: devops-team-slack
  namespace: kargo-demo
spec:
  slack:
    channel: "#devops"
    secretRef:
      name: devops-team-slack-secret
---
apiVersion: v1
kind: Secret
metadata:
  name: devops-team-slack-secret
  namespace: kargo-demo
stringData:
  token: xoxb-0000000000-0000000000000-XXXXXXXXXXXXXXXXXXXXXXXX
```

`types` and `when` are both optional; omitting them matches everything.

### Slack channels

Slack messages are posted with the
[`chat.postMessage`](https://api.slack.com/methods/chat.postMessage) API.
Create a Slack app with the `chat:write` scope, install it to your
workspace, invite the bot to the target channels, and store its bot token
(`xoxb-...`) under the `token` key of the referenced Secret. One token can
serve any number of `MessageChannel`s; each picks its own
`spec.slack.channel`.

Messages are rendered as mrkdwn with the event type, project, stage,
resource, and message, e.g.:

> :x: **Promotion Failed**
> **Project:** `kargo-demo`
> **Stage:** `production`
> **Resource:** `Promotion/prod.01jx2y8sw5fjvnnerqyqjwbb22`
> > Step 3 failed: ...

### Webhook channels

```yaml
apiVersion: kargo-event-router.io/v1alpha1
kind: MessageChannel
metadata:
  name: audit-webhook
  namespace: kargo-demo
spec:
  webhook:
    url: https://hooks.example.com/kargo
    secretRef:
      name: audit-webhook-secret   # optional; data key: secret (HMAC signing key)
```

### `when` expressions

`when` is an [expr-lang](https://expr-lang.org) expression (the same engine
Kargo uses) evaluated against an `event` object that exposes `type`,
`project`, `message`, and every Kargo event annotation as a camelCase field
(`stageName`, `promotionName`, `freightName`, `freightAlias`,
`analysisRunName`, `actor`, ...):

```
event.stageName == 'production'
event.type == 'PromotionFailed' && hasPrefix(event.promotionName, 'prod.')
event.message contains 'error-rate'
```

### Custom messages (`output`)

By default, message channels render a standard summary of the event. Set
`output` on a channel reference to replace it. `${{ }}` blocks contain
expr-lang expressions evaluated against the same `event` object as `when`:

```yaml
apiVersion: kargo-event-router.io/v1alpha1
kind: EventRouter
metadata:
  name: promotion-started
  namespace: kargo-demo
spec:
  types:
  - PromotionCreated
  channels:
  - name: devops-team-slack
    kind: MessageChannel
    output: "Kargo has kicked off promotion to stage: ${{ event.stageName }}."
```

`output` is per channel reference, so one router can deliver a custom
message to one channel and the default rendering to another. Webhook
channels always receive the full structured event and ignore `output`.

### Event types

`PromotionCreated`, `PromotionSucceeded`, `PromotionFailed`,
`PromotionErrored`, `PromotionAborted`, `FreightApproved`,
`FreightVerificationSucceeded`, `FreightVerificationFailed`,
`FreightVerificationErrored`, `FreightVerificationAborted`,
`FreightVerificationInconclusive`, `FreightVerificationUnknown`

### Webhook payload

Webhook channels receive events as CloudEvents 1.0 in structured JSON mode
with `Content-Type: application/cloudevents+json`:

```json
{
  "specversion": "1.0",
  "id": "f81d4fae-7dec-11d0-a765-00a0c91e6bf6",
  "source": "kargo/kargo-demo",
  "type": "io.akuity.kargo.freight-verification-failed",
  "subject": "Freight/3c9282127fb7ada33ec8c1546ab7ad21c1d646a5",
  "time": "2026-06-09T12:00:00Z",
  "datacontenttype": "application/json",
  "data": {
    "project": "kargo-demo",
    "name": "3c9282127fb7ada33ec8c1546ab7ad21c1d646a5",
    "stageName": "prod",
    "analysisRunName": "prod.01jx2y8sw5fjvnnerqyqjwbb22.3c92821",
    "message": "Metric \"error-rate\" assessed Failed due to failed (1) > failureLimit (0)"
  }
}
```

The `data` field carries the typed Kargo event (commits, images, charts,
verification timing, actor, etc.) exactly as Kargo models it.

### Verifying signatures

When `secretRef` is set, each request carries an
`X-Kargo-Event-Router-Signature` header of the form `sha256=<hex>`, an
HMAC-SHA256 of the raw request body keyed with the Secret's `secret` value:

```bash
echo -n "$BODY" | openssl dgst -sha256 -hmac "$SIGNING_KEY"
```

## Configuration

| Environment variable | Default | Description |
|---|---|---|
| `MAX_EVENT_AGE` | `30m` | Events older than this are never delivered. Prevents replaying history when the controller (re)starts. |
| `SEND_TIMEOUT` | `10s` | Per-request timeout for deliveries. |
| `METRICS_BIND_ADDRESS` | `:8080` | Bind address for the Prometheus metrics endpoint. Set to `0` to disable. |
| `HEALTH_PROBE_BIND_ADDRESS` | `:8081` | Bind address for `/healthz` and `/readyz`. |
| `ENABLE_LEADER_ELECTION` | `false` | Enable leader election when running more than one replica. |

## Metrics

Prometheus metrics are served on `METRICS_BIND_ADDRESS` at `/metrics` (the
bundled Deployment carries `prometheus.io/scrape` annotations). In addition
to the standard controller-runtime and Go runtime metrics:

| Metric | Type | Labels | Description |
|---|---|---|---|
| `kargo_event_router_deliveries_total` | counter | `project`, `channel`, `channel_type` (`slack`\|`webhook`\|`unknown`), `event_type`, `result` (`success`\|`error`) | Every delivery attempt. |
| `kargo_event_router_promotions_total` | counter | `project`, `stage`, `result` (`success`\|`failure`\|`error`\|`aborted`\|`created`) | Every Promotion event dispatched, counted once. |
| `kargo_event_router_freights_total` | counter | `project`, `stage`, `result` (`approved`) | Every non-verification Freight event dispatched, counted once. |
| `kargo_event_router_verifications_total` | counter | `project`, `stage`, `result` (`success`\|`failure`\|`error`\|`aborted`\|`inconclusive`\|`unknown`) | Every Freight verification event dispatched, counted once. |

The `deliveries_total` counter is delivery-centric: it increments once per
channel per attempt. The `promotions_total`, `freights_total`, and
`verifications_total` counters are event-centric: each event is counted
exactly once, when it is first dispatched to a channel, regardless of how many
channels receive it or how many times the reconcile is retried. Their `result`
label reflects the Kargo event outcome (e.g. a `PromotionSucceeded` event is
`result="success"`), not the delivery result. Every Promotion, Freight, and
Freight verification event increments exactly one of these three counters.

Useful queries:

```promql
# Messages sent successfully, by channel
sum by (channel) (rate(kargo_event_router_deliveries_total{result="success"}[5m]))

# Failed webhook deliveries
sum by (project, channel) (rate(kargo_event_router_deliveries_total{channel_type="webhook", result="error"}[5m]))

# Alert when any channel is failing
rate(kargo_event_router_deliveries_total{result="error"}[10m]) > 0

# Promotion failure rate by stage
sum by (project, stage) (rate(kargo_event_router_promotions_total{result="failure"}[5m]))

# Freight verification outcomes by stage
sum by (stage, result) (rate(kargo_event_router_verifications_total[5m]))
```

Note that failed deliveries are retried with backoff, so a single event can
contribute multiple `result="error"` increments to `deliveries_total` (and
eventually one `result="success"` if the destination recovers). The
`promotions_total`, `freights_total`, and `verifications_total` counters are
unaffected by retries.

## Caveats

- Kubernetes Events have a TTL (one hour by default). If the controller is
  down longer than that, events that expired in the meantime are lost. This
  is generally acceptable for notifications.
- Webhook deliveries refuse to connect to link-local addresses
  (169.254.0.0/16, fe80::/10) to mitigate SSRF against cloud metadata
  endpoints, reusing Kargo's `pkg/net` hardening.

## Development

```bash
make codegen   # regenerate deepcopy code and CRD manifests
make lint      # go vet
make test      # unit tests (-race)
make build     # build bin/kargo-event-router
```

The `github.com/akuity/kargo` main module references its nested `api` and
`pkg/client/generated` modules via local `replace` directives, which do not
apply to downstream consumers. This module therefore pins both nested modules
to the commit of the Kargo release it builds against (see the `replace`
directives in `go.mod`). When bumping the Kargo dependency, update those pins
to the new release's commit.

## Examples

See [`examples/`](examples/) for ready-to-apply manifests, including Slack
notifications for failed promotions.

## Roadmap

- `Ready` condition on EventRouter/MessageChannel status (URL/Secret
  validation before the first event fires)
- Additional channel types (SNS/SQS, NATS, Microsoft Teams)
