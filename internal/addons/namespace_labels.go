package addons

import (
	"fmt"
	"io"

	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

func EnsureDefaultNamespaceLabels(runner *util.Runner, monAgent, istio bool, clusterName string, out io.Writer) error {
	if !monAgent && !istio {
		return nil
	}
	ui.Step(out, "[%s] labeling default namespace for enabled addons...", clusterName)
	if monAgent {
		if err := runner.Run("kubectl label namespace default mon-agent/enabled=true --overwrite", out); err != nil {
			return fmt.Errorf("[%s] labeling default namespace for mon-agent: %w", clusterName, err)
		}
	}
	if istio {
		if err := runner.Run("kubectl label namespace default istio-injection=enabled --overwrite", out); err != nil {
			return fmt.Errorf("[%s] labeling default namespace for Istio injection: %w", clusterName, err)
		}
	}
	return nil
}
