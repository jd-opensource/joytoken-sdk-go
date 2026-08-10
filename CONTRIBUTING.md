# Contributing

Thank you for helping improve the JoyToken SDK for Go.

## Before opening a change

- Search existing issues and pull requests before starting substantial work.
- Do not include API keys, access tokens, customer data, or production responses in commits, tests, or issue reports.
- Keep public API changes documented and add regression tests for behavior changes.

## Local checks

```bash
gofmt -w *.go example/*.go example/live/*.go
go test ./...
go test -race ./...
go vet ./...
cd example && go test ./...
```

Pull requests should explain the user-visible behavior, compatibility impact, and test coverage. Contributions are licensed under the repository license unless a separate written agreement says otherwise.
