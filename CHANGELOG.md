# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/unict-cclab/proxmox-k3s/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/unict-cclab/proxmox-k3s/releases/tag/v0.1.0
