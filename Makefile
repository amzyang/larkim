.PHONY: build test vet lint snapshot

build:
	go build -o larkim ./cmd/larkim

test:
	go test ./...

vet:
	go vet ./...

snapshot:
	goreleaser release --snapshot --clean
