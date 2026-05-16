package cluster

import (
	"context"
	"fmt"
	"io"

	"golang.org/x/sync/errgroup"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	pxclient "github.com/unict-cclab/proxmox-k3s/internal/proxmox"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
)

// Delete removes all clusters defined in the config.
func Delete(ctx context.Context, cfg *config.Config, out io.Writer) error {
	return deleteMulti(ctx, cfg, out)
}

// Teardown removes everything in reverse order: clusters → registry → template.
// Errors are logged and execution continues so that a partially-broken environment
// can still be fully cleaned up.
func Teardown(ctx context.Context, cfg *config.Config, out io.Writer) error {
	if err := deleteMulti(ctx, cfg, out); err != nil {
		ui.Warn(out, "error deleting clusters (continuing): %v", err)
	}

	if cfg.Registry != nil {
		fmt.Fprintln(out)
		ui.Section(out, "=== Deleting registry ===")
		if err := DeleteRegistry(ctx, cfg, out); err != nil {
			ui.Warn(out, "error deleting registry (continuing): %v", err)
		}
	}

	if cfg.NFS != nil {
		fmt.Fprintln(out)
		ui.Section(out, "=== Deleting NFS server ===")
		if err := DeleteNFSServer(ctx, cfg, out); err != nil {
			ui.Warn(out, "error deleting NFS server (continuing): %v", err)
		}
	}

	if cfg.HasTemplateConfig() {
		px, err := pxclient.New(cfg)
		if err != nil {
			return err
		}
		fmt.Fprintln(out)
		ui.Section(out, "=== Deleting template ===")
		for _, name := range collectTemplateNames(cfg) {
			if err := pxclient.DeleteVM(ctx, px, name, out); err != nil {
				ui.Warn(out, "error deleting template %q (continuing): %v", name, err)
			}
		}
	}

	fmt.Fprintln(out)
	ui.Success(out, "Teardown complete")
	return nil
}

func deleteMulti(ctx context.Context, cfg *config.Config, out io.Writer) error {
	for _, spec := range cfg.Clusters {
		clusterCfg := cfg.ToClusterConfig(spec)

		fmt.Fprintln(out)
		ui.Section(out, fmt.Sprintf("=== Deleting cluster: %s ===", spec.Name))

		if err := deleteSingle(ctx, clusterCfg, out); err != nil {
			ui.Warn(out, "error deleting %s: %v (continuing)", spec.Name, err)
		}
	}

	fmt.Fprintln(out)
	ui.Success(out, "All clusters deleted")
	return nil
}

func deleteSingle(ctx context.Context, cfg *config.Config, out io.Writer) error {
	px, err := pxclient.New(cfg)
	if err != nil {
		return err
	}

	ui.Section(out, fmt.Sprintf("Finding VMs for cluster %q", cfg.ClusterName))
	vms, err := px.FindClusterVMs(ctx, cfg.ClusterName)
	if err != nil {
		return fmt.Errorf("listing cluster VMs: %w", err)
	}

	if len(vms) == 0 {
		ui.Info(out, "no cluster VMs found")
	} else {
		ui.Success(out, "found %d VM(s)", len(vms))
	}

	g, ctx := errgroup.WithContext(ctx)
	for _, vm := range vms {
		name := vm.Name
		g.Go(func() error {
			return pxclient.DeleteVM(ctx, px, name, out)
		})
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("deleting VMs: %w", err)
	}

	fmt.Fprintln(out)
	ui.Success(out, "Cluster %q deleted", cfg.ClusterName)
	return nil
}
