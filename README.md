# Proxmox K3s

[![CI](https://github.com/amarchese96/proxmox-k3s/actions/workflows/ci.yml/badge.svg)](https://github.com/amarchese96/proxmox-k3s/actions/workflows/ci.yml)
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

- **Single binary, single config file** — replaces the Packer + Terraform + Ansible stack
- **Idempotent** — re-running after a partial failure is always safe
- **Single-node or HA control plane** — 1 node for dev, 3 nodes with embedded etcd for production
- **Static IP or DHCP** — configurable per node
- **Flexible cloud images** — Ubuntu 24.04/22.04, Debian 12, or any custom image URL
- **Worker customisation** — per-node Kubernetes labels and taints applied at join time
- **Cross-platform** — Linux, macOS, and Windows binaries published with every release

## Install

Download the latest CLI binary from the GitHub Releases page:

https://github.com/amarchese96/proxmox-k3s/releases

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

See [cluster.example.yaml](cluster.example.yaml) for the configuration parameters

| Field | Default | Description |
|---|---|---|
| `cluster_name` | `k3s-cluster` | Name used to prefix all VM names and state files |
| `kubeconfig_path` | `./kubeconfig` | Where to write the kubeconfig after cluster creation |
| `proxmox.api_url` | — | Proxmox API endpoint, e.g. `https://host:8006/api2/json` |
| `proxmox.token_id` | — | API token ID, e.g. `root@pam!mytoken` |
| `proxmox.token_secret` | — | API token secret UUID |
| `proxmox.insecure_tls` | `false` | Skip TLS verification (common for self-signed PVE certs) |
| `template.name` | `<cluster_name>-tmpl` | VM template name; set explicitly to share across clusters |
| `template.proxmox_node` | first CP node | PVE node where the template is built |
| `template.storage` | first CP node | Storage for the template disk and cloud-init disk; must support `images` content type |
| `template.image_storage` | `template.storage` | Staging storage for the downloaded cloud image; needed for Ceph/RBD pools |
| `template.bridge` | first CP node | Network bridge for the template VM NIC used during the temporary template boot |
| `template.ip` | — | Optional static IP for the temporary template boot; leave empty to use DHCP |
| `template.gateway` | — | Gateway for `template.ip`; must be set together with `template.ip` |
| `template.dns` | `1.1.1.1` | DNS server for the temporary template boot when `template.gateway` is set |
| `template.subnet_mask` | `24` | CIDR prefix length for `template.ip` |
| `template.timeout_seconds` | `1800` | One timeout value used for cloud-image download, guest boot, IP/SSH wait, package refresh, shutdown, and template conversion |
| `template.os` | `ubuntu-24.04` | Resolves to the official cloud image; supported: `ubuntu-24.04`, `ubuntu-22.04`, `debian-12` |
| `template.cloud_image_url` | auto from `os` | Override to use a custom cloud image URL |
| `k3s.version` | latest stable | k3s version to install, e.g. `v1.32.3+k3s1` |
| `k3s.extra_server_args` | — | Extra flags appended to the k3s server install command |
| `k3s.extra_agent_args` | — | Extra flags appended to the k3s agent install command |
| `control_plane[].name` | — | Base VM name (required); the cluster name is automatically prefixed |
| `control_plane[].proxmox_node` | — | PVE node to create the VM on (required) |
| `control_plane[].storage` | `local-lvm` | VM disk storage; works with lvm-thin, zfs, ceph, dir |
| `control_plane[].bridge` | `vmbr0` | Network bridge |
| `control_plane[].cores` | `2` | vCPU count |
| `control_plane[].memory` | `2048` | RAM in MB |
| `control_plane[].disk_size` | `20` | Disk size in GB |
| `control_plane[].ip` | — | Static IP address; omit for DHCP |
| `control_plane[].gateway` | — | Default gateway (required when `ip` is set) |
| `control_plane[].dns` | `1.1.1.1` | DNS server |
| `control_plane[].subnet_mask` | `24` | CIDR prefix length |
| `workers[].name` | — | Base VM name (required); the cluster name is automatically prefixed |
| `workers[].proxmox_node` | — | PVE node to create the VM on (required) |
| `workers[].storage` | `local-lvm` | VM disk storage |
| `workers[].bridge` | `vmbr0` | Network bridge |
| `workers[].cores` | `2` | vCPU count |
| `workers[].memory` | `2048` | RAM in MB |
| `workers[].disk_size` | `20` | Disk size in GB |
| `workers[].ip` | — | Static IP address; omit for DHCP |
| `workers[].gateway` | — | Default gateway (required when `ip` is set) |
| `workers[].dns` | `1.1.1.1` | DNS server |
| `workers[].subnet_mask` | `24` | CIDR prefix length |
| `workers[].labels` | — | Kubernetes node labels to apply after join |
| `workers[].taints` | — | Kubernetes node taints to apply after join |

During `template create`, the image is booted once so the guest can run `apt-get update` and `apt-get dist-upgrade` before being converted into a reusable template. By default that temporary boot uses DHCP; if `template.ip` and `template.gateway` are set, it uses the static settings instead.

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
