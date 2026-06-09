# kargo-event-router

A standalone Kubernetes controller that routes [Kargo](https://kargo.io)
events — promotion failures, verification (AnalysisRun) failures, freight
approvals, and more — to external webhook endpoints as
[CloudEvents](https://cloudevents.io).

Kargo records everything that happens during promotion and verification as
Kubernetes Events with rich, structured annotations. This controller watches
those Events and delivers them to destinations you describe with `EventRoute`
resources, one or more per Project namespace. No changes to Kargo itself are
required.

## How it works

```
Kargo controllers / API server          kargo-event-router
┌───────────────────────────┐           ┌─────────────────────────────────┐
│ record Kargo events as    │  (etcd)   │ watch corev1.Event              │
│ corev1.Event (unchanged)  │ ────────▶ │  ├ match EventRoutes in the     │
└───────────────────────────┘           │  │  event's namespace           │
                                        │  ├ POST CloudEvents to webhooks │
                                        │  └ mark the event as routed     │
                                        └─────────────────────────────────┘
```

- The watch is restricted server-side (field selector on
  `involvedObject.apiVersion`) so only Kargo-related Events are cached.
- Typed event payloads are reconstructed using Kargo's own
  `github.com/akuity/kargo/pkg/event` library, so the JSON you receive mirrors
  Kargo's event model exactly.
- Delivery is **at-least-once**. Each successful delivery is recorded on the
  Event in a `kargo-event-router.io/routed-to` annotation, so retries (and
  controller restarts) never re-deliver to a route that already succeeded.
  Consumers can deduplicate using the CloudEvent `id`, which is the Event's
  UID.
- Failed deliveries are retried with exponential backoff by the controller's
  workqueue.

## Installation

```bash
kubectl apply -f config/crd -f config/rbac -f config/manager
```

The deployment runs a single replica. If you scale it up, set
`ENABLE_LEADER_ELECTION=true` to avoid duplicate deliveries.

## Usage

Create an `EventRoute` in a Project namespace. For example, to be notified
when verification (an Argo Rollouts `AnalysisRun` spawned by a Stage) fails,
or when a Promotion fails outright, for production Stages:

```yaml
apiVersion: kargo-event-router.io/v1alpha1
kind: EventRoute
metadata:
  name: alert-on-prod-failures
  namespace: kargo-demo
spec:
  eventTypes:
  - FreightVerificationFailed
  - FreightVerificationErrored
  - PromotionFailed
  - PromotionErrored
  stages:
  - prod
  webhook:
    url: https://hooks.example.com/kargo
    secretRef:
      name: alert-sink-secret
---
apiVersion: v1
kind: Secret
metadata:
  name: alert-sink-secret
  namespace: kargo-demo
stringData:
  secret: my-signing-key
```

Both `eventTypes` and `stages` are optional; an empty list matches
everything.

### Event types

`PromotionCreated`, `PromotionSucceeded`, `PromotionFailed`,
`PromotionErrored`, `PromotionAborted`, `FreightApproved`,
`FreightVerificationSucceeded`, `FreightVerificationFailed`,
`FreightVerificationErrored`, `FreightVerificationAborted`,
`FreightVerificationInconclusive`, `FreightVerificationUnknown`

### Payload

Events are POSTed as CloudEvents 1.0 in structured JSON mode with
`Content-Type: application/cloudevents+json`:

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
| `SEND_TIMEOUT` | `10s` | Per-request timeout for webhook deliveries. |
| `METRICS_BIND_ADDRESS` | `0` (disabled) | Bind address for the Prometheus metrics endpoint. |
| `HEALTH_PROBE_BIND_ADDRESS` | `:8081` | Bind address for `/healthz` and `/readyz`. |
| `ENABLE_LEADER_ELECTION` | `false` | Enable leader election when running more than one replica. |

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

## Roadmap

- `Ready` condition on EventRoute status (URL/Secret validation before the
  first event fires)
- Additional sink types (Slack, SNS/SQS, NATS)
- Glob/regex Stage selectors
