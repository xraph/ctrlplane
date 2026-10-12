.PHONY: f l test
f:
	GOWORK=off gofmt -w main.go
	goimports -w -local github.com/xraph/ctrlplane main.go
l:
	GOWORK=off golangci-lint run ./...
test:
	GOWORK=off go test -race -count=1 ./...
