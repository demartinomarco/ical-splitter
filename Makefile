.PHONY: fmt test vet check run build

fmt:
	go fmt ./...

test:
	go test ./...

vet:
	go vet ./...

check: fmt vet test

run:
	go run .

build:
	mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/ical-splitter .
