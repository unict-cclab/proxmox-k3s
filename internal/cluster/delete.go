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
func Delete(ctx context.Context, cfg *config.Config, deleteTemplate bool, out io.Writer) error {
	return deleteMulti(ctx, cfg, deleteTemplate, out)
}

func deleteMulti(ctx context.Context, cfg *config.Config, deleteTemplate bool, out io.Writer) error {
	last := len(cfg.Clusters) - 1

	for i, spec := range cfg.Clusters {
		clusterCfg := cfg.ToClusterConfig(spec)

		fmt.Fprintln(out)
		ui.Section(out, fmt.Sprintf("=== Deleting cluster: %s ===", spec.ClusterName))

		// Delete the shared template only with the last cluster.
		deleteThisTemplate := deleteTemplate && i == last

		if err := deleteSingle(ctx, clusterCfg, deleteThisTemplate, out); err != nil {
			ui.Warn(out, "error deleting %s: %v (continuing)", spec.ClusterName, err)
		}
	}

	fmt.Fprintln(out)
	ui.Success(out, "All clusters deleted")
	return nil
}

func deleteSingle(ctx context.Context, cfg *config.Config, deleteTemplate bool, out io.Writer) error {
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

	if deleteTemplate {
		ui.Section(out, "Deleting template")
		if err := pxclient.DeleteTemplate(ctx, px, cfg, out); err != nil {
			return err
		}
	}

	fmt.Fprintln(out)
	ui.Success(out, "Cluster %q deleted", cfg.ClusterName)
	return nil
}
