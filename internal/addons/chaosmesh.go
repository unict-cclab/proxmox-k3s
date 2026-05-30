package addons

import (
	"fmt"
	"io"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

const (
	chaosMeshRepo      = "https://charts.chaos-mesh.org"
	chaosMeshRelease   = "chaos-mesh"
	chaosMeshChart     = "chaos-mesh/chaos-mesh"
	chaosMeshNamespace = "chaos-mesh"
)

// chaosMeshValuesTemplate is the Helm values for Chaos Mesh on k3s.
// Format arg: dashboardNodePort (int).
const chaosMeshValuesTemplate = `chaosDaemon:
  runtime: containerd
  socketPath: /run/k3s/containerd/containerd.sock

controllerManager:
  replicaCount: 1
  affinity:
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

dashboard:
  enabled: true
  replicaCount: 1
  affinity:
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
  service:
    type: NodePort
    port: 2333
    nodePort: %d
  securityMode: false
`

// InstallChaosMesh installs Chaos Mesh via Helm using the k3s containerd socket path.
// The dashboard is exposed on addon.DashboardNodePort.
func InstallChaosMesh(runner *util.Runner, addon config.ChaosMeshConfig, clusterName string, out io.Writer) error {
	ui.Step(out, "[%s] adding chaos-mesh Helm repo...", clusterName)
	if err := helmAddRepo(runner, "chaos-mesh", chaosMeshRepo, out); err != nil {
		return err
	}

	values := fmt.Sprintf(chaosMeshValuesTemplate, addon.DashboardNodePort)
	chart := fmt.Sprintf("%s --version %s", chaosMeshChart, addon.Version)

	ui.Step(out, "[%s] installing Chaos Mesh %s (dashboard :%d)...", clusterName, addon.Version, addon.DashboardNodePort)
	if err := helmInstall(runner, chaosMeshRelease, chart, chaosMeshNamespace, values, "10m", out); err != nil {
		return err
	}

	ui.Success(out, "[%s] chaos-mesh ready — dashboard :%d", clusterName, addon.DashboardNodePort)
	return nil
}
