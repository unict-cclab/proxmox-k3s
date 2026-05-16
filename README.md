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
- [Working with pre-existing infrastructure](#working-with-pre-existing-infrastructure)
- [Configuration](#configuration)
- [Development](#development)
- [Contributing](#contributing)
- [License](#license)

## Features

- **Single binary, single config file** — replaces the Packer + Terraform + Ansible stack
- **Idempotent** — re-running after a partial failure is always safe
- **Single-node or HA control plane** — 1 node for dev, 3 nodes with embedded etcd for production
- **Multi-cluster + Cilium mesh** — provision multiple clusters and connect them automatically
- **Static IP or DHCP** — configurable per node, per-node template override
- **Flexible cloud images** — Ubuntu 24.04/22.04, Debian 12, or any custom image URL
- **Worker customisation** — per-node Kubernetes labels and taints applied at join time
- **Optional addons** — Cilium CNI, Hubble UI, kube-prometheus-stack, Istio, Harbor registry, NFS CSI driver
- **Cross-platform** — Linux, macOS, and Windows binaries published with every release

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
cp examples/clusters.yaml cluster.yaml
vi cluster.yaml
```

Create the cluster:

```bash
proxmox-k3s cluster create -c cluster.yaml
```

Access the cluster:

```bash
export KUBECONFIG=./kubeconfig
kubectl get nodes
```

Delete it when you are done:

```bash
proxmox-k3s cluster delete -c cluster.yaml
```

## Commands

All commands accept `-c <path>` (default: `cluster.yaml`).

### Infrastructure (provision once, shared across clusters)

```bash
proxmox-k3s template create   # build the VM template
proxmox-k3s template delete

proxmox-k3s registry create   # provision a Harbor pull-through cache
proxmox-k3s registry delete

proxmox-k3s nfs create        # provision an NFS server
proxmox-k3s nfs delete
```

### Cluster lifecycle

```bash
proxmox-k3s cluster create      # provision VMs, install k3s and addons
proxmox-k3s cluster delete      # stop and destroy cluster VMs
proxmox-k3s cluster mesh        # connect already-running clusters into a Cilium mesh
proxmox-k3s cluster kubeconfig  # re-fetch and save the kubeconfig
```

### All-in-one

```bash
proxmox-k3s setup-all    # provision all resources
proxmox-k3s delete-all   # tear down all resources
```

## Working with pre-existing infrastructure

If you already have a template, a Harbor registry, and/or an NFS server provisioned elsewhere, you can create clusters that reference them without reprovisioning anything.

**Cluster config** (`cluster.yaml`):

```yaml
clusters:
  - name: my-cluster
    addons:
      registry:
        hostname: 192.168.1.50   # pre-existing Harbor IP
        http_port: 80
      nfs:
        enabled: true
        server: 192.168.1.51     # pre-existing NFS server IP
        data_dir: /data/nfs
    control_plane:
      - name: cp-01
        template: ubuntu-24.04-template   # pre-existing template name
        ...
```

Then simply run:

```bash
proxmox-k3s cluster create -c cluster.yaml
```

The tool will SSH into the NFS server to configure the cluster's export directory automatically. For this to work, the **SSH key used to provision the NFS server must be present** at the path configured in `ssh_key_path` (default: `~/.proxmox-k3s/id_ed25519`).

If the NFS server was provisioned by someone else or by a different machine, place the corresponding private key at that path:

```bash
mkdir -p ~/.proxmox-k3s
cp /path/to/private-key ~/.proxmox-k3s/id_ed25519
chmod 600 ~/.proxmox-k3s/id_ed25519
```

Or set `ssh_key_path` in the config to point to your key directly.

## Configuration

| File | Purpose |
|------|---------|
| [examples/shared.yaml](examples/shared.yaml) | Infra provisioning: template, Harbor registry, NFS server |
| [examples/clusters.yaml](examples/clusters.yaml) | Cluster provisioning: two clusters + Cilium mesh |

Infra-only commands (`template`, `registry`, `nfs`) only need the `proxmox`, `template`, `registry`, and `nfs` top-level sections — no `clusters` block required.

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
