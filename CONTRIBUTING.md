# Contributing

## Development

Use the Go version declared in `go.mod`.

Before submitting changes, run:

```sh
go mod tidy
go test ./...
go test -race ./...
go mod verify
go tool golangci-lint run ./...
go tool govulncheck ./...
```

## Compatibility

Keep public APIs backward compatible within a major version. Breaking changes
belong in a new major module path.
