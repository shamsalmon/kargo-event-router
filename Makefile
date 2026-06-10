CONTROLLER_GEN := go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0

.PHONY: codegen
codegen: ## Regenerate deepcopy code and CRD manifests
	$(CONTROLLER_GEN) object paths=./api/...
	$(CONTROLLER_GEN) crd paths=./api/... output:crd:artifacts:config=config/crd
	cp config/crd/*.yaml charts/kargo-event-router/crds/

.PHONY: lint
lint: ## Vet Go code
	go vet ./...

.PHONY: test
test: ## Run unit tests
	go test -race ./...

.PHONY: build
build: ## Build the controller binary
	CGO_ENABLED=0 go build -o bin/kargo-event-router ./cmd/kargo-event-router

.PHONY: install
install: ## Install CRD, RBAC, and the controller into the current cluster
	kubectl apply -f config/crd -f config/rbac -f config/manager

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "%-10s %s\n", $$1, $$2}'
