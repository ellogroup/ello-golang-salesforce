.PHONY: validate
validate: format static-tests unit-tests

.PHONY: format
format:
	gofmt -w ./
	go mod tidy

.PHONY: static-tests
static-tests:
	golangci-lint run -v
	gosec ./...
	govulncheck ./...

.PHONY: unit-tests
unit-tests:
	go test -v -cover ./...