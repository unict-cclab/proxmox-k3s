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

// monitoringValuesTemplate is the Helm values template for kube-prometheus-stack.
// Format args: grafanaNodePort (int), grafanaAdminPassword, prometheusNodePort (int).
const monitoringValuesTemplate = `alertmanager:
  enabled: false

grafana:
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
    nodePort: %d
  adminPassword: "%s"
  persistence:
    enabled: true
    type: sts
    storageClassName: local-path
    accessModes:
      - ReadWriteOnce
    size: 1Gi

prometheusOperator:
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

prometheus:
  service:
    type: NodePort
    nodePort: %d
  prometheusSpec:
    retention: 7d
    scrapeInterval: "5s"
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
    storageSpec:
      volumeClaimTemplate:
        spec:
          storageClassName: local-path
          accessModes: ["ReadWriteOnce"]
          resources:
            requests:
              storage: 5Gi
prometheus-node-exporter:
  prometheus:
    monitor:
      relabelings:
        - action: replace
          sourceLabels:
            - __meta_kubernetes_pod_node_name
          targetLabel: instance
`

// InstallMonitoring installs kube-prometheus-stack via Helm.
// Prometheus is exposed on addon.PrometheusNodePort and Grafana on addon.GrafanaNodePort.
// Both use the local-path storage class that k3s ships with by default.
func InstallMonitoring(runner *util.Runner, addon config.MonitoringConfig, clusterName string, out io.Writer) error {
	ui.Step(out, "[%s] adding prometheus-community Helm repo...", clusterName)
	if err := helmAddRepo(runner, "prometheus-community", prometheusCommRepo, out); err != nil {
		return err
	}

	values := fmt.Sprintf(monitoringValuesTemplate,
		addon.GrafanaNodePort,
		addon.GrafanaAdminPassword,
		addon.PrometheusNodePort,
	)

	chart := fmt.Sprintf("%s --version %s", monitoringChart, addon.Version)
	ui.Step(out, "[%s] installing kube-prometheus-stack %s (Prometheus :%d, Grafana :%d)...",
		clusterName, addon.Version, addon.PrometheusNodePort, addon.GrafanaNodePort)
	if err := helmInstall(runner, monitoringRelease, chart, monitoringNamespace, values, "20m", out); err != nil {
		return err
	}

	ui.Success(out, "[%s] monitoring ready — Prometheus :%d  Grafana :%d (admin/%s)",
		clusterName, addon.PrometheusNodePort, addon.GrafanaNodePort, addon.GrafanaAdminPassword)
	return nil
}
