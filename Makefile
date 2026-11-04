.DEFAULT_GOAL := help
.PHONY: help web build test install

help: ## Show this help
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  make %-8s %s\n", $$1, $$2}'

web: ## Build the frontend into web/dist
	cd web && npm ci && npm run build

build: web ## Build the frontend, then the gh-design-review binary
	go build -o gh-design-review .

test: ## Run Go and frontend tests
	go test ./...
	cd web && npm test

install: build ## Build and (re)install this checkout as the `gh design-review` extension
	-gh extension remove design-review
	gh extension install .
