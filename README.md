# Proxmox K3s

[![CI](https://github.com/unict-cclab/proxmox-k3s/actions/workflows/ci.yml/badge.svg)](https://github.com/unict-cclab/proxmox-k3s/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Provision and manage k3s clusters on Proxmox VE with a single CLI and a single YAML config file.

## Table of Contents

- [Features](#features)
- [Install](#install)
- [Requirements](#requirements)
- [Quick Start](#quick-start)
- [Commands](#commands)
- [Configuration](#configuration)
- [Development](#development)
- [Contributing](#contributing)
- [License](#license)

## Features

- **Single binary, single config file** â€” replaces the Packer + Terraform + Ansible stack
- **Idempotent** â€” re-running after a partial failure is always safe
- **Single-node or HA control plane** â€” 1 node for dev, 3 nodes with embedded etcd for production
- **Static IP or DHCP** â€” configurable per node
- **Flexible cloud images** â€” Ubuntu 24.04/22.04, Debian 12, or any custom image URL
- **Worker customisation** â€” per-node Kubernetes labels and taints applied at join time
- **Cross-platform** â€” Linux, macOS, and Windows binaries published with every release

## Install

Download the latest CLI binary from the GitHub Releases page:

https://github.com/unict-cclab/proxmox-k3s/releases

Choose the archive for your platform, extract it, and place `proxmox-k3s` somewhere in your `PATH`.

## Requirements

- Proxmox VE 8 or 9
- A Proxmox API token with permissions for VM lifecycle and storage operations

## Quick Start

Copy the example config and edit it:

```bash
cp cluster.example.yaml cluster.yaml
vi cluster.yaml
```

Create the cluster:

```bash
proxmox-k3s create -c cluster.yaml
```

Access the cluster:

```bash
export KUBECONFIG=./kubeconfig
kubectl get nodes
```

Delete it when you are done:

```bash
proxmox-k3s delete -c cluster.yaml
```

## Commands

```text
proxmox-k3s create
proxmox-k3s delete
proxmox-k3s kubeconfig
proxmox-k3s template create
proxmox-k3s template delete
```

All commands accept `-c <path>` and default to `cluster.yaml`.

## Configuration

See [cluster.example.yaml](cluster.example.yaml) for a fully-annotated example of all available options.

## Development

```bash
make build   # build the binary
make test    # run tests
make lint    # run golangci-lint
make clean   # remove the binary
```

Or directly with Go:

```bash
go build -o proxmox-k3s ./cmd/main.go
go test ./...
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines on reporting bugs, proposing features, and submitting pull requests.

## License

[MIT](LICENSE)
