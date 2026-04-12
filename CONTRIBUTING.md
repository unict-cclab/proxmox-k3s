# Contributing

Thank you for your interest in contributing to proxmox-k3s!

## Prerequisites

- Go 1.21 or later
- A Proxmox VE 8/9 environment (for integration testing)
- [golangci-lint](https://golangci-lint.run/usage/install/) for linting

## Development setup

```bash
git clone https://github.com/amarchese96/proxmox-k3s.git
cd proxmox-k3s
go mod download
```

Build the binary:

```bash
make build
# or: go build -o proxmox-k3s ./cmd/main.go
```

Run tests:

```bash
make test
# or: go test ./...
```

Run the linter:

```bash
make lint
# or: golangci-lint run
```

## Making changes

1. Fork the repository and create a branch from `main`.
2. Write your code. Add or update tests where appropriate.
3. Make sure `make test` and `make lint` both pass.
4. Open a pull request against `main`. Fill in the PR template.

## Commit style

Use short, imperative commit messages:

```
add support for debian-12 cloud image
fix SSH timeout on slow Proxmox nodes
docs: update cluster.example.yaml defaults
```

## Reporting bugs

Please open a GitHub issue using the **Bug report** template and include:

- Your Proxmox VE version
- The proxmox-k3s version (`proxmox-k3s --version`)
- A minimal `cluster.yaml` that reproduces the problem (remove secrets)
- The full error output

## Requesting features

Open a GitHub issue using the **Feature request** template.

## Code of conduct

Be respectful. Constructive feedback is welcome; personal attacks are not.
