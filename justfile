default:
    @just --list

# CGO_ENABLED=0 matches the release build. It also routes kooky's Keychain read
# through /usr/bin/security, whose "Always Allow" survives a rebuild; a cgo build
# asks from larkim itself, and an ad-hoc signature is a new app every build.
build:
    CGO_ENABLED=0 go build -o larkim ./cmd/larkim

# 构建并进入 TUI（dev.yaml 不在仓库中，需自建）
run: build
    ./larkim --config ./dev.yaml

test:
    go test ./...

vet:
    go vet ./...

snapshot:
    goreleaser release --snapshot --clean
