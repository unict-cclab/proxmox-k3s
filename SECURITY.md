# Security Policy

## Supported versions

Only the latest release receives security fixes.

## Reporting a vulnerability

Please **do not** open a public GitHub issue for security vulnerabilities.

Report them privately via [GitHub Security Advisories](https://github.com/amarchese96/proxmox-k3s/security/advisories/new).

Include:

- A description of the vulnerability and its potential impact
- Steps to reproduce or a proof-of-concept
- Any suggested mitigations

You will receive an acknowledgement within 72 hours. Once the issue is confirmed and a fix is prepared, a patched release will be published and you will be credited in the advisory (unless you prefer to remain anonymous).

## Scope

This project runs with local network access to a Proxmox API endpoint and manages VM lifecycle. Credentials are read from `cluster.yaml` and are never transmitted beyond the configured Proxmox API URL.

Key security considerations:

- **Proxmox API tokens**: store `cluster.yaml` with restricted file permissions (`chmod 600`). Never commit it to version control.
- **SSH keys**: key pairs generated per cluster are stored in `~/.proxmox-k3s/<cluster-name>/`. Protect this directory.
- **TLS**: `insecure_tls: true` disables certificate verification. Only use this on trusted internal networks.
