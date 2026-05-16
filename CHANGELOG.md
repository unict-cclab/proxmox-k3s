# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-05-16

### Added
- Multi-cluster support: a single config file can define multiple clusters provisioned concurrently
- `cluster create` / `cluster delete` / `cluster kubeconfig` / `cluster mesh` subcommands grouping all cluster lifecycle operations
- `setup-all` command to provision all resources (template, registry, NFS, clusters) in one shot
- `delete-all` command to tear down all resources in reverse order
- Harbor registry VM provisioning (`registry create` / `registry delete`) with automatic containerd pull-through cache configuration for docker.io, ghcr.io, gcr.io, quay.io, and registry.k8s.io
- NFS server VM provisioning (`nfs create` / `nfs delete`) with per-cluster export directories
- NFS CSI driver addon for Kubernetes dynamic PVC provisioning via NFS
- Cilium CNI addon with optional Hubble UI NodePort exposure
- Cilium cluster mesh (`cluster mesh`) connecting clusters via shared CA and cross-cluster service discovery
- kube-prometheus-stack addon (Prometheus + Grafana) with configurable NodePort access
- Istio service mesh addon: Gateway API CRDs, `istio/base`, and `istiod` control plane
- Gateway API CRD version auto-resolved from the latest stable GitHub release when not pinned
- Prometheus `PodMonitor` (Envoy sidecar) and `ServiceMonitor` (istiod) applied automatically when both Istio and monitoring addons are enabled
- `qemu-guest-agent` installed in the VM template during preparation, enabling reliable IP detection via the Proxmox guest agent API
- `PrefixWriter` utility for per-node log prefixing during parallel provisioning
- `examples/shared.yaml`, `examples/single-cluster.yaml`, `examples/multi-cluster.yaml` example configs
- `cluster.example.yaml` at repository root documenting every available config option

### Changed
- CLI restructured: cluster lifecycle commands are now subcommands of `cluster`; `mesh` moved from top-level to `cluster mesh`
- `setup-all` skips template creation and runs template verification instead when no `template:` block is present in the config
- SSH user (`ubuntu`) unified as `config.VMSSHUser` exported constant, eliminating duplicates across packages
- Multi-line YAML strings in addon packages extracted to named package-level constants (fixes indentation break in Go source)
- k3s containerd registry mirror config extracted to a named constant in `k3s/install.go`

### Fixed
- `setup-all` no longer attempts to create a VM template when running against a clusters-only config
- Linting: ineffectual `ctx` assignment in `installWorkers`, `misspell` false positives on `exportfs` (Linux binary), British English `serialises` → `serializes`

## [0.2.0] - 2026-04-12

### Added
- `disk_size` and `nameserver` parameters for VM nodes
- VMs configured to start on boot

## [0.1.0] - 2026-04-12

### Added
- `create` command: provisions VMs on Proxmox, installs k3s, writes kubeconfig (idempotent)
- `delete` command: stops and removes all cluster VMs; optional `--template` flag
- `kubeconfig` command: re-fetches and saves the kubeconfig from the control plane
- `template create` / `template delete` subcommands for managing the base VM template
- Support for Ubuntu 24.04, Ubuntu 22.04, and Debian 12 cloud images
- Static IP and DHCP support per node
- HA control plane via embedded etcd (1 or 3 control-plane nodes)
- Worker node labels and taints applied post-join
- Cross-platform release binaries: Linux amd64/arm64, macOS amd64/arm64, Windows amd64

[Unreleased]: https://github.com/unict-cclab/proxmox-k3s/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/unict-cclab/proxmox-k3s/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/unict-cclab/proxmox-k3s/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/unict-cclab/proxmox-k3s/releases/tag/v0.1.0
