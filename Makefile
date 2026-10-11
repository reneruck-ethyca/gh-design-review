.PHONY: build web test

web:
	cd web && npm ci && npm run build

build: web
	go build -o gh-design-review .

test:
	go test ./...
	cd web && npm test
