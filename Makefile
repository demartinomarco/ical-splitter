.PHONY: fmt test cover vet check run build clean-cover

COVERAGE_DIR ?= coverage
COVERAGE_PROFILE := $(COVERAGE_DIR)/coverage.out
COVERAGE_HTML := $(COVERAGE_DIR)/coverage.html

fmt:
	go fmt ./...

test:
	go test -cover ./...

cover:
	mkdir -p $(COVERAGE_DIR)
	go test -covermode=count -coverprofile=$(COVERAGE_PROFILE) ./...
	go tool cover -func=$(COVERAGE_PROFILE)
	go tool cover -html=$(COVERAGE_PROFILE) -o $(COVERAGE_HTML)
	@echo "HTML coverage report: $(COVERAGE_HTML)"

vet:
	go vet ./...

check: fmt vet test

run:
	go run .

build:
	mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/ical-splitter .

clean-cover:
	rm -rf $(COVERAGE_DIR)
