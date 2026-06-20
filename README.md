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
- [HPA scaling event logs](#hpa-scaling-event-logs)
- [Addon access ports](#addon-access-ports)
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
- **Optional addons** — Cilium CNI, Hubble UI, kube-prometheus-stack, Loki + Alloy logging, Istio, Harbor registry, NFS CSI driver
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
cp cluster.example.yaml cluster.yaml
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
      logging:
        enabled: true
        loki_node_port: 32099
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

## HPA scaling event logs

Enable `addons.logging` to install Loki and Alloy in the `observability` namespace. Alloy watches Kubernetes events, keeps HPA rescale events, and pushes them to Loki. Loki is exposed through the configured NodePort, so external tools can query:

```bash
curl "http://<node-ip>:32099/loki/api/v1/query_range?query={job=\"kubernetes-events\"}%20|%20json"
```

## Addon access ports

Most addon UIs and APIs are exposed with Kubernetes NodePorts, so they are reachable at `http://<node-ip>:<node-port>` from any cluster node IP.

| Addon | Service | Config key | Default | URL |
|------|---------|------------|---------|-----|
| Cilium | Hubble UI | `addons.cilium.hubble_ui_node_port` | `32080` | `http://<node-ip>:32080` |
| Cilium cluster mesh | ClusterMesh API | Not configurable | `32379` when cluster mesh is enabled | Internal Cilium mesh access |
| Monitoring | Prometheus | `addons.monitoring.prometheus_node_port` | `32090` | `http://<node-ip>:32090` |
| Monitoring | Grafana | `addons.monitoring.grafana_node_port` | `32000` | `http://<node-ip>:32000` |
| Cluster Lens | Topology UI | `addons.cluster_lens.node_port` | `32088` | `http://<node-ip>:32088` |
| Logging | Loki API | `addons.logging.loki_node_port` | `32099` | `http://<node-ip>:32099` |
| Istio tracing | Jaeger UI | `addons.jaeger.node_port` | `30002` | `http://<node-ip>:30002` |
| Istio console | Kiali UI | `addons.kiali.node_port` | `30001` | `http://<node-ip>:30001` |
| Chaos Mesh | Dashboard | `addons.chaos_mesh.dashboard_node_port` | `32300` | `http://<node-ip>:32300` |

When monitoring is enabled, Grafana is also provisioned with a `Sophos` folder. It includes an `Application Metrics` dashboard with namespace/group filters, request metrics, p95 response time, replicas, CPU, and memory panels, plus an `Infrastructure Metrics` dashboard with Mentat latency, packet-loss, available-bandwidth, and node CPU/memory panels. Cluster Lens provides the infrastructure topology and reads mon-agent annotations including CPU, memory, `disk-throughput`, `network-throughput`, and the per-node Mentat link metrics. The application traffic panels use Istio metrics when Istio scraping is enabled.

Enable `addons.mon_agent` to deploy mon-agent into `observability`. The installer labels the `default` namespace with `mon-agent/enabled=true` so default-namespace deployments are annotated automatically. When Istio is enabled, the installer also labels `default` with `istio-injection=enabled` for Envoy sidecar injection.

Harbor and NFS are provisioned as separate infrastructure VMs rather than Kubernetes NodePort services. Mentat exposes Prometheus metrics inside the cluster and is scraped automatically when monitoring is enabled.

## Configuration

| File | Purpose |
|------|---------|
| [examples/shared.yaml](examples/shared.yaml) | Infra provisioning: template, Harbor registry, NFS server |
| [cluster.example.yaml](cluster.example.yaml) | Full multi-cluster provisioning example with addons |
| [examples/single-cluster.yaml](examples/single-cluster.yaml) | Minimal single-cluster provisioning example |

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
