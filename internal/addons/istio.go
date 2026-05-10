package addons

import (
	"fmt"
	"io"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

const (
	istioRepo      = "https://istio-release.storage.googleapis.com/charts"
	istioNamespace = "istio-system"
)

// InstallIstio installs Istio via Helm: istio/base (CRDs) then istio/istiod (control plane).
func InstallIstio(runner *util.Runner, istio config.IstioConfig, clusterName string, out io.Writer) error {
	ui.Step(out, "[%s] adding Istio Helm repo...", clusterName)
	if err := helmAddRepo(runner, "istio", istioRepo, out); err != nil {
		return err
	}

	ui.Step(out, "[%s] installing Istio base CRDs %s...", clusterName, istio.Version)
	baseChart := fmt.Sprintf("istio/base --version %s", istio.Version)
	if err := helmInstall(runner, "istio-base", baseChart, istioNamespace, "{}", "", out); err != nil {
		return fmt.Errorf("[%s] istio-base: %w", clusterName, err)
	}

	// istiod is the Istio control plane (pilot, citadel, galley).
	// Preferred affinity steers it to the management node pool when one is present;
	// the toleration lets it run there if the pool carries a nodepool=management:NoSchedule taint.
	// affinity and tolerations are root-level values in the istiod chart (no pilot: wrapper).
	values := `affinity:
  nodeAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
    - weight: 100
      preference:
        matchExpressions:
        - key: nodepool
          operator: In
          values:
          - management
tolerations:
- key: "nodepool"
  operator: "Equal"
  value: "management"
  effect: "NoSchedule"
`

	ui.Step(out, "[%s] installing istiod %s...", clusterName, istio.Version)
	istiodChart := fmt.Sprintf("istio/istiod --version %s", istio.Version)
	if err := helmInstall(runner, "istiod", istiodChart, istioNamespace, values, "", out); err != nil {
		return fmt.Errorf("[%s] istiod: %w", clusterName, err)
	}

	ui.Success(out, "[%s] Istio %s ready", clusterName, istio.Version)
	return nil
}
