package addons

import (
	"fmt"
	"io"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

const (
	prometheusCommRepo  = "https://prometheus-community.github.io/helm-charts"
	monitoringRelease   = "prometheus-stack"
	monitoringChart     = "prometheus-community/kube-prometheus-stack"
	monitoringNamespace = "observability"
)

// InstallMonitoring installs kube-prometheus-stack via Helm.
// Prometheus is exposed on addon.PrometheusNodePort and Grafana on addon.GrafanaNodePort.
// Both use the local-path storage class that k3s ships with by default.
func InstallMonitoring(runner *util.Runner, addon config.MonitoringAddon, clusterName string, out io.Writer) error {
	ui.Step(out, "[%s] adding prometheus-community Helm repo...", clusterName)
	if err := helmAddRepo(runner, "prometheus-community", prometheusCommRepo); err != nil {
		return err
	}

	values := fmt.Sprintf(`alertmanager:
  enabled: false

grafana:
  service:
    type: NodePort
    nodePort: %d
  adminPassword: "%s"
  persistence:
    enabled: true
    type: sts
    storageClassName: local-path
    accessModes:
      - ReadWriteOnce
    size: 1Gi

prometheusOperator: {}

prometheus:
  service:
    type: NodePort
    nodePort: %d
  prometheusSpec:
    retention: 7d
    scrapeInterval: "5s"
    storageSpec:
      volumeClaimTemplate:
        spec:
          storageClassName: local-path
          accessModes: ["ReadWriteOnce"]
          resources:
            requests:
              storage: 5Gi
`,
		addon.GrafanaNodePort,
		addon.GrafanaAdminPassword,
		addon.PrometheusNodePort,
	)

	ui.Step(out, "[%s] installing kube-prometheus-stack (Prometheus :%d, Grafana :%d)...",
		clusterName, addon.PrometheusNodePort, addon.GrafanaNodePort)
	if err := helmInstall(runner, monitoringRelease, monitoringChart, monitoringNamespace, values, out); err != nil {
		return err
	}

	ui.Success(out, "[%s] monitoring ready — Prometheus :%d  Grafana :%d (admin/%s)",
		clusterName, addon.PrometheusNodePort, addon.GrafanaNodePort, addon.GrafanaAdminPassword)
	return nil
}
