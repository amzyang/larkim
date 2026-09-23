default:
    @just --list

build:
    go build -o larkim ./cmd/larkim

# 构建并进入 TUI（dev.yaml 不在仓库中，需自建）
run: build
    ./larkim --config ./dev.yaml tui

test:
    go test ./...

vet:
    go vet ./...

snapshot:
    goreleaser release --snapshot --clean
