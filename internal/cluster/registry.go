package cluster

import (
	"context"
	"fmt"
	"io"

	"github.com/unict-cclab/proxmox-k3s/internal/addons"
	"github.com/unict-cclab/proxmox-k3s/internal/config"
	pxclient "github.com/unict-cclab/proxmox-k3s/internal/proxmox"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

// CreateRegistry provisions the Harbor VM and installs Harbor.
// Returns the mirror endpoint (e.g. "http://192.168.100.10:80").
// Idempotent: if the VM already exists, returns the endpoint without reprovisioning.
func CreateRegistry(ctx context.Context, cfg *config.Config, out io.Writer) (string, error) {
	keyPath, err := cfg.SSHKeyFilePath()
	if err != nil {
		return "", fmt.Errorf("SSH key path: %w", err)
	}
	keyPair, err := util.EnsureKeyPairAt(keyPath)
	if err != nil {
		return "", fmt.Errorf("SSH key pair: %w", err)
	}
	px, err := pxclient.New(cfg)
	if err != nil {
		return "", err
	}

	if err := checkTemplateExists(ctx, px, cfg); err != nil {
		return "", err
	}

	existing, _ := px.FindVMByName(ctx, cfg.Registry.VM.Name)
	if existing != nil {
		endpoint := fmt.Sprintf("http://%s:%d", cfg.Registry.Harbor.Hostname, cfg.Registry.Harbor.HTTPPort)
		// VM already exists — still ensure proxy projects are created (idempotent).
		runner, err := util.DialWithKey(cfg.Registry.Harbor.Hostname, 22, "ubuntu", keyPath)
		if err != nil {
			return endpoint, fmt.Errorf("SSH to registry VM: %w", err)
		}
		defer runner.Close()
		if err := addons.EnsureProxyProjects(runner, cfg.Registry.Harbor, out); err != nil {
			return endpoint, fmt.Errorf("harbor: proxy projects: %w", err)
		}
		return endpoint, nil
	}

	vmid, err := px.NextVMID(ctx, cfg.Template.VMIDBase+1)
	if err != nil {
		return "", fmt.Errorf("allocating registry VMID: %w", err)
	}
	return setupRegistry(ctx, cfg, px, vmid, keyPair, out)
}

// DeleteRegistry removes the Harbor VM.
func DeleteRegistry(ctx context.Context, cfg *config.Config, out io.Writer) error {
	px, err := pxclient.New(cfg)
	if err != nil {
		return err
	}
	return pxclient.DeleteVM(ctx, px, cfg.Registry.VM.Name, out)
}
