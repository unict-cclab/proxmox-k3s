# Proxmox K3s

Provision and manage k3s clusters on Proxmox VE with a single CLI and a single YAML config file.

## Install

Download the latest CLI binary from the GitHub Releases page:

https://github.com/amarchese96/proxmox-k3s/releases

Choose the archive for your platform, extract it, and place `proxmox-k3s` somewhere in your `PATH`.

## Requirements

- Proxmox VE 7 or 8
- A Proxmox API token with permissions for VM lifecycle and storage operations
- A cloud image with QEMU guest agent enabled

## Quick Start

Copy the example config and edit it:

```bash
cp cluster.example.yaml cluster.yaml
$EDITOR cluster.yaml
```

The minimum required fields are:

```yaml
proxmox:
  api_url: https://<proxmox-host>:8006/api2/json
  token_id: root@pam!mytoken
  token_secret: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx

node_defaults:
  proxmox_node: pve1
```

Create the cluster:

```bash
proxmox-k3s create -c cluster.yaml
```

Use the cluster:

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

See [cluster.example.yaml](cluster.example.yaml) for the full reference.

Useful details:

- `template.name` lets multiple clusters share the same VM template
- if that template already exists, creation is skipped
- `template.storage` is the final template disk storage
- `template.image_storage` is the file-based staging storage used to download the cloud image
- `template.cloud_init_storage` must support VM images
- `k3s.extra_server_args` is where all server-side k3s flags should go

## Development

This repository currently has two main layers:

- `api/` for reusable cluster operations
- `cmd/` for the CLI wrapper

Build from source:

```bash
go build -o proxmox-k3s ./cmd/main.go
```

Run tests:

```bash
go test ./...
```
