# Contributing to Nexss Transport

Nexss Transport provides protocol adapters and boundary bridges for the Nexss ecosystem.

Before opening a pull request, run:

```bash
gofmt -w .
go test ./...
go test -race ./...
go vet ./...
go test -run='^$' -bench=. -benchmem ./...
```

Keep protocol implementations decoupled, test with race detection enabled, and verify zero-allocation claims on memory hotpaths.
