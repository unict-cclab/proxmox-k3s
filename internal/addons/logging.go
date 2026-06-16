package addons

import (
	"fmt"
	"io"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

const (
	grafanaRepo      = "https://grafana.github.io/helm-charts"
	loggingNamespace = "observability"
	lokiRelease      = "loki"
	lokiChart        = "grafana/loki"
	alloyRelease     = "alloy-events"
	alloyChart       = "grafana/alloy"
)

// loggingLokiValuesTemplate configures Loki in single-binary mode for small k3s clusters.
// Format args: retention, storageSize, lokiNodePort.
const loggingLokiValuesTemplate = `deploymentMode: SingleBinary

loki:
  auth_enabled: false
  commonConfig:
    replication_factor: 1
  storage:
    type: filesystem
  schemaConfig:
    configs:
      - from: "2024-04-01"
        store: tsdb
        object_store: filesystem
        schema: v13
        index:
          prefix: loki_index_
          period: 24h
  limits_config:
    retention_period: %s
  compactor:
    retention_enabled: true
    delete_request_store: filesystem

singleBinary:
  replicas: 1
  persistence:
    enabled: true
    storageClass: local-path
    size: %s
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
  - key: "ManagementOnly"
    operator: "Equal"
    value: "true"
    effect: "NoSchedule"

read:
  replicas: 0
write:
  replicas: 0
backend:
  replicas: 0

gateway:
  enabled: true
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
  - key: "ManagementOnly"
    operator: "Equal"
    value: "true"
    effect: "NoSchedule"
  service:
    type: NodePort
    nodePort: %d

chunksCache:
  enabled: false
resultsCache:
  enabled: false
lokiCanary:
  enabled: false
test:
  enabled: false
minio:
  enabled: false
`

// loggingAlloyValuesTemplate configures Alloy to watch Kubernetes events and keep
// only events related to HPA rescaling decisions before writing them to Loki.
const loggingAlloyValuesTemplate = `controller:
  type: deployment
  replicas: 1
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
  - key: "ManagementOnly"
    operator: "Equal"
    value: "true"
    effect: "NoSchedule"

alloy:
  configMap:
    create: true
    content: |-
      loki.source.kubernetes_events "hpa" {
        job_name   = "kubernetes-events"
        log_format = "json"
        forward_to = [loki.process.hpa_scaling_events.receiver]
      }

      loki.process "hpa_scaling_events" {
        forward_to = [loki.write.local.receiver]

        stage.match {
          selector            = "{job=\"kubernetes-events\"} !~ \"HorizontalPodAutoscaler|SuccessfulRescale\""
          action              = "drop"
          drop_counter_reason = "not_hpa_scaling_event"
        }
      }

      loki.write "local" {
        endpoint {
          url = "http://loki-gateway.observability.svc.cluster.local/loki/api/v1/push"
        }
      }
`

// InstallLogging installs Loki plus Alloy. Alloy collects Kubernetes events from
// the API server and forwards HPA scaling events to Loki. Loki's gateway is
// exposed as a NodePort so external tools can query /loki/api/v1/query_range.
func InstallLogging(runner *util.Runner, addon config.LoggingConfig, clusterName string, out io.Writer) error {
	ui.Step(out, "[%s] adding Grafana Helm repo...", clusterName)
	if err := helmAddRepo(runner, "grafana", grafanaRepo, out); err != nil {
		return err
	}

	lokiValues := fmt.Sprintf(loggingLokiValuesTemplate, addon.Retention, addon.StorageSize, addon.LokiNodePort)
	lokiChartWithVersion := fmt.Sprintf("%s --version %s", lokiChart, addon.LokiVersion)
	ui.Step(out, "[%s] installing Loki %s (API :%d)...", clusterName, addon.LokiVersion, addon.LokiNodePort)
	if err := helmInstall(runner, lokiRelease, lokiChartWithVersion, loggingNamespace, lokiValues, "20m", out); err != nil {
		return err
	}

	alloyChartWithVersion := fmt.Sprintf("%s --version %s", alloyChart, addon.AlloyVersion)
	ui.Step(out, "[%s] installing Alloy %s for HPA scaling events...", clusterName, addon.AlloyVersion)
	if err := helmInstall(runner, alloyRelease, alloyChartWithVersion, loggingNamespace, loggingAlloyValuesTemplate, "10m", out); err != nil {
		return err
	}

	ui.Success(out, "[%s] logging ready — Loki API :%d", clusterName, addon.LokiNodePort)
	return nil
}
