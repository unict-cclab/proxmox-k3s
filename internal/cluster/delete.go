package cluster

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/amarchese96/proxmox-k3s/internal/config"
	pxclient "github.com/amarchese96/proxmox-k3s/internal/proxmox"
	"github.com/amarchese96/proxmox-k3s/internal/ui"
)

func Delete(ctx context.Context, cfg *config.Config, deleteTemplate bool, out io.Writer) error {
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

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for _, vm := range vms {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			if err := pxclient.DeleteVM(ctx, px, name, out); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(vm.Name)
	}
	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("errors during VM deletion: %v", errs)
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
