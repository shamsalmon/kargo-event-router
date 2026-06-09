FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /kargo-event-router ./cmd/kargo-event-router

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /kargo-event-router /usr/local/bin/kargo-event-router
USER 65534:65534
ENTRYPOINT ["/usr/local/bin/kargo-event-router"]
