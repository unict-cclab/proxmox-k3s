package addons

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

const (
	istioRepo                = "https://istio-release.storage.googleapis.com/charts"
	istioNamespace           = "istio-system"
	gatewayAPICRDURLTemplate = "https://github.com/kubernetes-sigs/gateway-api/releases/download/%s/standard-install.yaml"
)

// istiodValuesTemplate is the Helm values for the istiod control plane chart.
const istiodValuesTemplate = `affinity:
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

// istioMonitorManifest contains the PodMonitor for Envoy sidecar proxies and the
// ServiceMonitor for the istiod control plane. Applied only when both Istio and
// the monitoring stack are enabled.
const istioMonitorManifest = `apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: envoy-stats-monitor
  namespace: istio-system
  labels:
    monitoring: istio-proxies
    release: prometheus-stack
spec:
  selector:
    matchExpressions:
    - {key: istio-prometheus-ignore, operator: DoesNotExist}
  namespaceSelector:
    any: true
  jobLabel: envoy-stats
  podMetricsEndpoints:
  - path: /stats/prometheus
    interval: 15s
    relabelings:
    - action: keep
      sourceLabels: [__meta_kubernetes_pod_container_name]
      regex: "istio-proxy"
    - action: keep
      sourceLabels: [__meta_kubernetes_pod_annotationpresent_prometheus_io_scrape]
    - action: replace
      regex: (\d+);(([A-Fa-f0-9]{1,4}::?){1,7}[A-Fa-f0-9]{1,4})
      replacement: '[$2]:$1'
      sourceLabels:
      - __meta_kubernetes_pod_annotation_prometheus_io_port
      - __meta_kubernetes_pod_ip
      targetLabel: __address__
    - action: replace
      regex: (\d+);((([0-9]+?)(\.|$)){4})
      replacement: $2:$1
      sourceLabels:
      - __meta_kubernetes_pod_annotation_prometheus_io_port
      - __meta_kubernetes_pod_ip
      targetLabel: __address__
    - action: labeldrop
      regex: "__meta_kubernetes_pod_label_(.+)"
    - sourceLabels: [__meta_kubernetes_namespace]
      action: replace
      targetLabel: namespace
    - sourceLabels: [__meta_kubernetes_pod_name]
      action: replace
      targetLabel: pod
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: istio-component-monitor
  namespace: istio-system
  labels:
    monitoring: istio-components
    release: prometheus-stack
spec:
  jobLabel: istio
  targetLabels: [app]
  selector:
    matchExpressions:
    - {key: istio, operator: In, values: [pilot]}
  namespaceSelector:
    any: true
  endpoints:
  - port: http-monitoring
    interval: 15s
`

// LatestGatewayAPIVersion queries the GitHub releases API for the latest stable Gateway API version tag.
func LatestGatewayAPIVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/kubernetes-sigs/gateway-api/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching latest Gateway API version: %w", err)
	}
	defer resp.Body.Close()
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("parsing GitHub response: %w", err)
	}
	if release.TagName == "" {
		return "", fmt.Errorf("could not parse tag_name from GitHub response")
	}
	return release.TagName, nil
}

// InstallIstio installs Gateway API CRDs then Istio via Helm: istio/base (CRDs) then istio/istiod (control plane).
func InstallIstio(runner *util.Runner, istio config.IstioConfig, clusterName string, out io.Writer) error {
	gwVersion := istio.GatewayAPIVersion
	if gwVersion == "" {
		var err error
		gwVersion, err = LatestGatewayAPIVersion(context.Background())
		if err != nil {
			return fmt.Errorf("[%s] resolving latest Gateway API version: %w", clusterName, err)
		}
	}
	gwURL := fmt.Sprintf(gatewayAPICRDURLTemplate, gwVersion)
	ui.Step(out, "[%s] installing Gateway API CRDs %s...", clusterName, gwVersion)
	if err := runner.Run(fmt.Sprintf("kubectl apply -f %s", gwURL), out); err != nil {
		return fmt.Errorf("[%s] Gateway API CRDs: %w", clusterName, err)
	}

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
	ui.Step(out, "[%s] installing istiod %s...", clusterName, istio.Version)
	istiodChart := fmt.Sprintf("istio/istiod --version %s", istio.Version)
	if err := helmInstall(runner, "istiod", istiodChart, istioNamespace, istiodValuesTemplate, "", out); err != nil {
		return fmt.Errorf("[%s] istiod: %w", clusterName, err)
	}

	ui.Success(out, "[%s] Istio %s ready", clusterName, istio.Version)
	return nil
}

// InstallIstioMonitors applies the PodMonitor and ServiceMonitor for Istio metrics
// scraping. Must be called only when both Istio and the monitoring stack are enabled.
func InstallIstioMonitors(runner *util.Runner, clusterName string, out io.Writer) error {
	ui.Step(out, "[%s] applying Istio PodMonitor and ServiceMonitor...", clusterName)
	if err := runner.WriteFile("/tmp/istio-monitors.yaml", []byte(istioMonitorManifest)); err != nil {
		return fmt.Errorf("[%s] writing Istio monitors: %w", clusterName, err)
	}
	if err := runner.Run("kubectl apply -f /tmp/istio-monitors.yaml", out); err != nil {
		return fmt.Errorf("[%s] applying Istio monitors: %w", clusterName, err)
	}
	ui.Success(out, "[%s] Istio monitors ready", clusterName)
	return nil
}
