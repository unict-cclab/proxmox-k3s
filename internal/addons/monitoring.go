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
  sidecar:
    dashboards:
      enabled: true
      searchNamespace: ALL
      folderAnnotation: grafana_folder
      provider:
        allowUiUpdates: true
        foldersFromFilesStructure: true
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
  adminPassword: "%s"
  persistence:
    enabled: true
    type: sts
    storageClassName: local-path
    accessModes:
      - ReadWriteOnce
    size: 1Gi

kube-state-metrics:
  metricLabelsAllowlist:
    - deployments=[group,app]
    - pods=[group,app]
  metricAnnotationsAllowList:
    - nodes=[cpu-usage,memory-usage,disk-throughput,network-throughput]
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
  - key: "ManagementOnly"
    operator: "Equal"
    value: "true"
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
    - key: "ManagementOnly"
      operator: "Equal"
      value: "true"
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

const loadGenGrafanaDashboardManifest = `apiVersion: v1
kind: ConfigMap
metadata:
  name: sophos-application-dashboard
  namespace: observability
  annotations:
    grafana_folder: Sophos
  labels:
    grafana_dashboard: "1"
    app.kubernetes.io/name: sophos-application-dashboard
data:
  sophos-application-dashboard.json: |-
    {
      "uid": "sophos-app-metrics",
      "title": "Application Metrics",
      "tags": ["sophos", "application", "istio", "kubernetes"],
      "timezone": "browser",
      "schemaVersion": 39,
      "version": 1,
      "refresh": "5s",
      "time": {"from": "now-1h", "to": "now"},
      "templating": {
        "list": [
          {
            "name": "namespace",
            "type": "query",
            "datasource": {"type": "prometheus", "uid": "prometheus"},
            "query": "label_values(kube_deployment_labels, namespace)",
            "refresh": 1,
            "sort": 1,
            "multi": false,
            "includeAll": false
          },
          {
            "name": "group",
            "type": "query",
            "datasource": {"type": "prometheus", "uid": "prometheus"},
            "query": "label_values(kube_deployment_labels{namespace=~\"$namespace\"}, label_group)",
            "refresh": 1,
            "sort": 1,
            "multi": false,
            "includeAll": false
          },
          {
            "name": "p95_window",
            "type": "custom",
            "query": "1m,5m,10m,30m",
            "current": {"selected": true, "text": "1m", "value": "1m"},
            "multi": false,
            "includeAll": false
          },
          {
            "name": "scheduler_profile",
            "type": "query",
            "datasource": {"type": "prometheus", "uid": "prometheus"},
            "query": "label_values(scheduler_schedule_attempts_total, profile)",
            "refresh": 1,
            "sort": 1,
            "multi": true,
            "includeAll": true,
            "current": {"selected": true, "text": "All", "value": "$__all"}
          }
        ]
      },
      "panels": [
        {
          "id": 1,
          "title": "RPS - Overall",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "ingress",
              "expr": "sum(rate(istio_requests_total{reporter=\"destination\",destination_workload_namespace=~\"$namespace\",source_workload=~\"istio-gateway-istio|gateway-.*\",destination_workload=\"frontend\"}[1m]) * on(destination_workload_namespace,destination_workload) group_left(label_group) label_replace(label_replace(kube_deployment_labels{namespace=~\"$namespace\",label_group=~\"$group\"} @ end(), \"destination_workload\", \"$1\", \"deployment\", \"(.*)\"), \"destination_workload_namespace\", \"$1\", \"namespace\", \"(.*)\"))"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "reqps"}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "single"}}
        },
        {
          "id": 2,
          "title": "RPS - By Service",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 12, "y": 0},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{destination_workload}}",
              "expr": "sum by (destination_workload) (rate(istio_requests_total{reporter=\"destination\",destination_workload_namespace=~\"$namespace\"}[1m]) * on(destination_workload_namespace,destination_workload) group_left(label_group) label_replace(label_replace(kube_deployment_labels{namespace=~\"$namespace\",label_group=~\"$group\"} @ end(), \"destination_workload\", \"$1\", \"deployment\", \"(.*)\"), \"destination_workload_namespace\", \"$1\", \"namespace\", \"(.*)\"))"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "reqps"}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi"}}
        },
        {
          "id": 3,
          "title": "Failures/s - Overall",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 0, "y": 8},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "ingress",
              "expr": "sum(rate(istio_requests_total{reporter=\"destination\",destination_workload_namespace=~\"$namespace\",source_workload=~\"istio-gateway-istio|gateway-.*\",destination_workload=\"frontend\",response_code!~\"2..|3..\"}[1m]) * on(destination_workload_namespace,destination_workload) group_left(label_group) label_replace(label_replace(kube_deployment_labels{namespace=~\"$namespace\",label_group=~\"$group\"} @ end(), \"destination_workload\", \"$1\", \"deployment\", \"(.*)\"), \"destination_workload_namespace\", \"$1\", \"namespace\", \"(.*)\"))"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "reqps"}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "single"}}
        },
        {
          "id": 4,
          "title": "Failures/s - By Service",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 12, "y": 8},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{destination_workload}}",
              "expr": "sum by (destination_workload) (rate(istio_requests_total{reporter=\"destination\",destination_workload_namespace=~\"$namespace\",response_code!~\"2..|3..\"}[1m]) * on(destination_workload_namespace,destination_workload) group_left(label_group) label_replace(label_replace(kube_deployment_labels{namespace=~\"$namespace\",label_group=~\"$group\"} @ end(), \"destination_workload\", \"$1\", \"deployment\", \"(.*)\"), \"destination_workload_namespace\", \"$1\", \"namespace\", \"(.*)\"))"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "reqps"}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi"}}
        },
        {
          "id": 5,
          "title": "P95 Response Time - Overall",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 0, "y": 16},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "ingress",
              "expr": "histogram_quantile(0.95, sum by (le) (rate(istio_request_duration_milliseconds_bucket{reporter=\"destination\",destination_workload_namespace=~\"$namespace\",source_workload=~\"istio-gateway-istio|gateway-.*\",destination_workload=\"frontend\"}[$p95_window]) * on(destination_workload_namespace,destination_workload) group_left(label_group) label_replace(label_replace(kube_deployment_labels{namespace=~\"$namespace\",label_group=~\"$group\"} @ end(), \"destination_workload\", \"$1\", \"deployment\", \"(.*)\"), \"destination_workload_namespace\", \"$1\", \"namespace\", \"(.*)\")))"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "ms"}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "single"}}
        },
        {
          "id": 6,
          "title": "P95 Response Time - By Service",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 12, "y": 16},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{destination_workload}}",
              "expr": "histogram_quantile(0.95, sum by (destination_workload, le) (rate(istio_request_duration_milliseconds_bucket{reporter=\"destination\",destination_workload_namespace=~\"$namespace\"}[$p95_window]) * on(destination_workload_namespace,destination_workload) group_left(label_group) label_replace(label_replace(kube_deployment_labels{namespace=~\"$namespace\",label_group=~\"$group\"} @ end(), \"destination_workload\", \"$1\", \"deployment\", \"(.*)\"), \"destination_workload_namespace\", \"$1\", \"namespace\", \"(.*)\")))"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "ms"}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi"}}
        },
        {
          "id": 7,
          "title": "Replicas - Overall",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 0, "y": 24},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "overall",
              "expr": "sum(kube_deployment_status_replicas{namespace=~\"$namespace\"} * on(namespace,deployment) group_left(label_group) kube_deployment_labels{namespace=~\"$namespace\",label_group=~\"$group\"} @ end())"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "short", "decimals": 0}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "single"}}
        },
        {
          "id": 8,
          "title": "Replicas - By Service",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 12, "y": 24},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{deployment}}",
              "expr": "kube_deployment_status_replicas{namespace=~\"$namespace\"} * on(namespace,deployment) group_left(label_group) kube_deployment_labels{namespace=~\"$namespace\",label_group=~\"$group\"} @ end()"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "short", "decimals": 0}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi"}}
        },
        {
          "id": 9,
          "title": "CPU Usage - Overall",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 0, "y": 32},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "overall",
              "expr": "sum(rate(container_cpu_usage_seconds_total{namespace=~\"$namespace\",container!=\"\",image!=\"\"}[1m]) * on(namespace,pod) group_left(label_group,label_app) kube_pod_labels{namespace=~\"$namespace\",label_group=~\"$group\"} @ end())"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "cores"}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "single"}}
        },
        {
          "id": 10,
          "title": "CPU Usage - By Service",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 12, "y": 32},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{label_app}}",
              "expr": "sum by (label_app) (rate(container_cpu_usage_seconds_total{namespace=~\"$namespace\",container!=\"\",image!=\"\"}[1m]) * on(namespace,pod) group_left(label_group,label_app) kube_pod_labels{namespace=~\"$namespace\",label_group=~\"$group\"} @ end())"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "cores"}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi"}}
        },
        {
          "id": 11,
          "title": "Memory Usage - Overall",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 0, "y": 40},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "overall",
              "expr": "sum(container_memory_working_set_bytes{namespace=~\"$namespace\",container!=\"\",image!=\"\"} * on(namespace,pod) group_left(label_group,label_app) kube_pod_labels{namespace=~\"$namespace\",label_group=~\"$group\"} @ end())"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "bytes"}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "single"}}
        },
        {
          "id": 12,
          "title": "Memory Usage - By Service",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 12, "y": 40},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{label_app}}",
              "expr": "sum by (label_app) (container_memory_working_set_bytes{namespace=~\"$namespace\",container!=\"\",image!=\"\"} * on(namespace,pod) group_left(label_group,label_app) kube_pod_labels{namespace=~\"$namespace\",label_group=~\"$group\"} @ end())"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "bytes"}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi"}}
        },
        {
          "id": 13,
          "title": "Scheduler Attempts",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 0, "y": 48},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{profile}} / {{result}}",
              "expr": "sum by (profile, result) (rate(scheduler_schedule_attempts_total{profile=~\"$scheduler_profile\"}[1m]))"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "ops"}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi"}}
        },
        {
          "id": 14,
          "title": "Scheduling Attempt P95",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 12, "y": 48},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{profile}} / {{result}}",
              "expr": "histogram_quantile(0.95, sum by (profile, result, le) (rate(scheduler_scheduling_attempt_duration_seconds_bucket{profile=~\"$scheduler_profile\"}[1m])))"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "s"}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi"}}
        },
        {
          "id": 15,
          "title": "Pod Scheduling SLI P95",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 0, "y": 56},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{attempts}} attempts",
              "expr": "histogram_quantile(0.95, sum by (attempts, le) (rate(scheduler_pod_scheduling_sli_duration_seconds_bucket[1m])))"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "s"}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi"}}
        },
        {
          "id": 16,
          "title": "Scheduler Framework Extension P95",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 12, "y": 56},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{profile}} / {{extension_point}} / {{status}}",
              "expr": "histogram_quantile(0.95, sum by (profile, extension_point, status, le) (rate(scheduler_framework_extension_point_duration_seconds_bucket{profile=~\"$scheduler_profile\"}[1m])))"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "s"}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi"}}
        },
        {
          "id": 17,
          "title": "Descheduler Evictions",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 0, "y": 64},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{profile}} / {{strategy}} / {{result}}",
              "expr": "sum by (profile, strategy, result) (rate(descheduler_pods_evicted_total{namespace=~\"$namespace\"}[1m]))"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "ops"}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi"}}
        },
        {
          "id": 18,
          "title": "Descheduler Duration P95",
          "type": "timeseries",
          "gridPos": {"h": 8, "w": 12, "x": 12, "y": 64},
          "targets": [
            {
              "refId": "A",
              "legendFormat": "loop",
              "expr": "histogram_quantile(0.95, sum by (le) (rate(descheduler_loop_duration_seconds_bucket[1m]))) or histogram_quantile(0.95, sum by (le) (rate(descheduler_descheduler_loop_duration_seconds_bucket[1m])))"
            },
            {
              "refId": "B",
              "legendFormat": "{{profile}} / {{strategy}}",
              "expr": "histogram_quantile(0.95, sum by (profile, strategy, le) (rate(descheduler_strategy_duration_seconds_bucket[1m]))) or histogram_quantile(0.95, sum by (profile, strategy, le) (rate(descheduler_descheduler_strategy_duration_seconds_bucket[1m])))"
            }
          ],
          "fieldConfig": {"defaults": {"unit": "s"}, "overrides": []},
          "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi"}}
        }
      ]
    }
`

const infrastructureGrafanaDashboardManifest = `apiVersion: v1
kind: ConfigMap
metadata:
  name: sophos-node-dashboard
  namespace: observability
  annotations:
    grafana_folder: Sophos
  labels:
    grafana_dashboard: "1"
    app.kubernetes.io/name: sophos-node-dashboard
data:
  sophos-node-dashboard.json: |-
    {
      "uid": "sophos-node-metrics",
      "title": "Infrastructure Metrics",
      "tags": [
        "sophos",
        "infrastructure",
        "mentat",
        "kubernetes"
      ],
      "timezone": "browser",
      "schemaVersion": 39,
      "version": 2,
      "refresh": "5s",
      "time": {
        "from": "now-1h",
        "to": "now"
      },
      "templating": {
        "list": [
          {
            "name": "origin_node",
            "type": "query",
            "datasource": {
              "type": "prometheus",
              "uid": "prometheus"
            },
            "query": "label_values(node_latency_count, origin_node)",
            "refresh": 1,
            "sort": 1,
            "multi": true,
            "includeAll": true,
            "current": {
              "selected": true,
              "text": "All",
              "value": "$__all"
            }
          },
          {
            "name": "destination_node",
            "type": "query",
            "datasource": {
              "type": "prometheus",
              "uid": "prometheus"
            },
            "query": "label_values(node_latency_count{origin_node=~\"$origin_node\"}, destination_node)",
            "refresh": 1,
            "sort": 1,
            "multi": true,
            "includeAll": true,
            "current": {
              "selected": true,
              "text": "All",
              "value": "$__all"
            }
          },
          {
            "name": "node",
            "type": "query",
            "datasource": {
              "type": "prometheus",
              "uid": "prometheus"
            },
            "query": "label_values(node_uname_info, instance)",
            "refresh": 1,
            "sort": 1,
            "multi": true,
            "includeAll": true,
            "current": {
              "selected": true,
              "text": "All",
              "value": "$__all"
            }
          },
          {
            "name": "latency_window",
            "type": "custom",
            "query": "1m,5m,10m,30m",
            "current": {
              "selected": true,
              "text": "1m",
              "value": "1m"
            },
            "multi": false,
            "includeAll": false
          }
        ]
      },
      "panels": [
        {
          "id": 2,
          "title": "Node Latency Mean Matrix",
          "type": "table",
          "gridPos": {
            "h": 8,
            "w": 24,
            "x": 0,
            "y": 0
          },
          "targets": [
            {
              "refId": "A",
              "format": "table",
              "instant": true,
              "expr": "1000 * sum by (origin_node, destination_node) (rate(node_latency_sum{origin_node=~\"$origin_node\",destination_node=~\"$destination_node\"}[$latency_window])) / sum by (origin_node, destination_node) (rate(node_latency_count{origin_node=~\"$origin_node\",destination_node=~\"$destination_node\"}[$latency_window]))"
            }
          ],
          "fieldConfig": {
            "defaults": {
              "unit": "ms",
              "decimals": 3
            },
            "overrides": []
          },
          "options": {
            "showHeader": true
          }
        },
        {
          "id": 3,
          "title": "Node Latency Mean",
          "type": "timeseries",
          "gridPos": {
            "h": 8,
            "w": 12,
            "x": 0,
            "y": 8
          },
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{origin_node}} -> {{destination_node}}",
              "expr": "1000 * sum by (origin_node, destination_node) (rate(node_latency_sum{origin_node=~\"$origin_node\",destination_node=~\"$destination_node\"}[$latency_window])) / sum by (origin_node, destination_node) (rate(node_latency_count{origin_node=~\"$origin_node\",destination_node=~\"$destination_node\"}[$latency_window]))"
            }
          ],
          "fieldConfig": {
            "defaults": {
              "unit": "ms"
            },
            "overrides": []
          },
          "options": {
            "legend": {
              "displayMode": "list",
              "placement": "bottom"
            },
            "tooltip": {
              "mode": "multi"
            }
          }
        },
        {
          "id": 4,
          "title": "Node Latency P95",
          "type": "timeseries",
          "gridPos": {
            "h": 8,
            "w": 12,
            "x": 12,
            "y": 8
          },
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{origin_node}} -> {{destination_node}}",
              "expr": "1000 * histogram_quantile(0.95, sum by (origin_node, destination_node, le) (rate(node_latency_bucket{origin_node=~\"$origin_node\",destination_node=~\"$destination_node\"}[$latency_window])))"
            }
          ],
          "fieldConfig": {
            "defaults": {
              "unit": "ms"
            },
            "overrides": []
          },
          "options": {
            "legend": {
              "displayMode": "list",
              "placement": "bottom"
            },
            "tooltip": {
              "mode": "multi"
            }
          }
        },
        {
          "id": 14,
          "title": "Inter-node Packet Loss",
          "type": "timeseries",
          "gridPos": {
            "h": 8,
            "w": 12,
            "x": 0,
            "y": 16
          },
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{origin_node}} -> {{destination_node}}",
              "expr": "100 * node_packet_loss_ratio{origin_node=~\"$origin_node\",destination_node=~\"$destination_node\"}"
            }
          ],
          "fieldConfig": {
            "defaults": {
              "unit": "percent",
              "min": 0,
              "max": 100
            },
            "overrides": []
          },
          "options": {
            "legend": {
              "displayMode": "list",
              "placement": "bottom"
            },
            "tooltip": {
              "mode": "multi"
            }
          }
        },
        {
          "id": 15,
          "title": "Available Inter-node Bandwidth",
          "type": "timeseries",
          "gridPos": {
            "h": 8,
            "w": 12,
            "x": 12,
            "y": 16
          },
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{origin_node}} -> {{destination_node}}",
              "expr": "node_bandwidth_bytes_per_second{origin_node=~\"$origin_node\",destination_node=~\"$destination_node\"}"
            }
          ],
          "fieldConfig": {
            "defaults": {
              "unit": "Bps",
              "min": 0
            },
            "overrides": []
          },
          "options": {
            "legend": {
              "displayMode": "list",
              "placement": "bottom"
            },
            "tooltip": {
              "mode": "multi"
            }
          }
        },
        {
          "id": 5,
          "title": "Node CPU Usage - Cores",
          "type": "timeseries",
          "gridPos": {
            "h": 8,
            "w": 12,
            "x": 0,
            "y": 24
          },
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{instance}}",
              "expr": "sum by (instance) (rate(node_cpu_seconds_total{mode!=\"idle\",instance=~\"$node\"}[1m]))"
            }
          ],
          "fieldConfig": {
            "defaults": {
              "unit": "cores"
            },
            "overrides": []
          },
          "options": {
            "legend": {
              "displayMode": "list",
              "placement": "bottom"
            },
            "tooltip": {
              "mode": "multi"
            }
          }
        },
        {
          "id": 6,
          "title": "Node CPU Usage - Percent",
          "type": "timeseries",
          "gridPos": {
            "h": 8,
            "w": 12,
            "x": 12,
            "y": 24
          },
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{instance}}",
              "expr": "100 * sum by (instance) (rate(node_cpu_seconds_total{mode!=\"idle\",instance=~\"$node\"}[1m])) / count by (instance) (node_cpu_seconds_total{mode=\"idle\",instance=~\"$node\"})"
            }
          ],
          "fieldConfig": {
            "defaults": {
              "unit": "percent"
            },
            "overrides": []
          },
          "options": {
            "legend": {
              "displayMode": "list",
              "placement": "bottom"
            },
            "tooltip": {
              "mode": "multi"
            }
          }
        },
        {
          "id": 7,
          "title": "Node Memory Usage - Bytes",
          "type": "timeseries",
          "gridPos": {
            "h": 8,
            "w": 12,
            "x": 0,
            "y": 32
          },
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{instance}}",
              "expr": "node_memory_MemTotal_bytes{instance=~\"$node\"} - node_memory_MemAvailable_bytes{instance=~\"$node\"}"
            }
          ],
          "fieldConfig": {
            "defaults": {
              "unit": "bytes"
            },
            "overrides": []
          },
          "options": {
            "legend": {
              "displayMode": "list",
              "placement": "bottom"
            },
            "tooltip": {
              "mode": "multi"
            }
          }
        },
        {
          "id": 8,
          "title": "Node Memory Usage - Percent",
          "type": "timeseries",
          "gridPos": {
            "h": 8,
            "w": 12,
            "x": 12,
            "y": 32
          },
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{instance}}",
              "expr": "100 * (1 - (node_memory_MemAvailable_bytes{instance=~\"$node\"} / node_memory_MemTotal_bytes{instance=~\"$node\"}))"
            }
          ],
          "fieldConfig": {
            "defaults": {
              "unit": "percent"
            },
            "overrides": []
          },
          "options": {
            "legend": {
              "displayMode": "list",
              "placement": "bottom"
            },
            "tooltip": {
              "mode": "multi"
            }
          }
        },
        {
          "id": 9,
          "title": "Node CPU Capacity - Cores",
          "type": "timeseries",
          "gridPos": {
            "h": 8,
            "w": 12,
            "x": 0,
            "y": 40
          },
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{instance}}",
              "expr": "count by (instance) (node_cpu_seconds_total{mode=\"idle\",instance=~\"$node\"})"
            }
          ],
          "fieldConfig": {
            "defaults": {
              "unit": "cores",
              "decimals": 0
            },
            "overrides": []
          },
          "options": {
            "legend": {
              "displayMode": "list",
              "placement": "bottom"
            },
            "tooltip": {
              "mode": "multi"
            }
          }
        },
        {
          "id": 10,
          "title": "Node Memory Capacity - Bytes",
          "type": "timeseries",
          "gridPos": {
            "h": 8,
            "w": 12,
            "x": 12,
            "y": 40
          },
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{instance}}",
              "expr": "node_memory_MemTotal_bytes{instance=~\"$node\"}"
            }
          ],
          "fieldConfig": {
            "defaults": {
              "unit": "bytes"
            },
            "overrides": []
          },
          "options": {
            "legend": {
              "displayMode": "list",
              "placement": "bottom"
            },
            "tooltip": {
              "mode": "multi"
            }
          }
        },
        {
          "id": 11,
          "title": "Cluster CPU Usage - Percent",
          "type": "timeseries",
          "gridPos": {
            "h": 8,
            "w": 12,
            "x": 0,
            "y": 48
          },
          "targets": [
            {
              "refId": "A",
              "legendFormat": "cluster",
              "expr": "100 * sum(rate(node_cpu_seconds_total{mode!=\"idle\",instance=~\"$node\"}[1m])) / count(node_cpu_seconds_total{mode=\"idle\",instance=~\"$node\"})"
            }
          ],
          "fieldConfig": {
            "defaults": {
              "unit": "percent"
            },
            "overrides": []
          },
          "options": {
            "legend": {
              "displayMode": "list",
              "placement": "bottom"
            },
            "tooltip": {
              "mode": "single"
            }
          }
        },
        {
          "id": 12,
          "title": "Cluster Memory Usage - Percent",
          "type": "timeseries",
          "gridPos": {
            "h": 8,
            "w": 12,
            "x": 12,
            "y": 48
          },
          "targets": [
            {
              "refId": "A",
              "legendFormat": "cluster",
              "expr": "100 * (1 - (sum(node_memory_MemAvailable_bytes{instance=~\"$node\"}) / sum(node_memory_MemTotal_bytes{instance=~\"$node\"})))"
            }
          ],
          "fieldConfig": {
            "defaults": {
              "unit": "percent"
            },
            "overrides": []
          },
          "options": {
            "legend": {
              "displayMode": "list",
              "placement": "bottom"
            },
            "tooltip": {
              "mode": "single"
            }
          }
        },
        {
          "id": 13,
          "title": "Mentat Samples/s",
          "type": "timeseries",
          "gridPos": {
            "h": 8,
            "w": 24,
            "x": 0,
            "y": 56
          },
          "targets": [
            {
              "refId": "A",
              "legendFormat": "{{origin_node}} -> {{destination_node}}",
              "expr": "sum by (origin_node, destination_node) (rate(node_latency_count{origin_node=~\"$origin_node\",destination_node=~\"$destination_node\"}[$latency_window]))"
            }
          ],
          "fieldConfig": {
            "defaults": {
              "unit": "ops"
            },
            "overrides": []
          },
          "options": {
            "legend": {
              "displayMode": "list",
              "placement": "bottom"
            },
            "tooltip": {
              "mode": "multi"
            }
          }
        }
      ],
      "editable": true
    }
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
	if err := InstallSophosDashboards(runner, clusterName, out); err != nil {
		return err
	}

	ui.Success(out, "[%s] monitoring ready — Prometheus :%d  Grafana :%d (admin/%s)",
		clusterName, addon.PrometheusNodePort, addon.GrafanaNodePort, addon.GrafanaAdminPassword)
	return nil
}

func InstallSophosDashboards(runner *util.Runner, clusterName string, out io.Writer) error {
	ui.Step(out, "[%s] installing Sophos Grafana dashboards...", clusterName)
	if err := runner.WriteFile("/tmp/load-gen-dashboard.yaml", []byte(loadGenGrafanaDashboardManifest)); err != nil {
		return fmt.Errorf("[%s] writing Sophos application dashboard: %w", clusterName, err)
	}
	if err := runner.Run("kubectl apply -f /tmp/load-gen-dashboard.yaml", out); err != nil {
		return fmt.Errorf("[%s] applying Sophos application dashboard: %w", clusterName, err)
	}
	if err := runner.WriteFile("/tmp/sophos-infrastructure-dashboard.yaml", []byte(infrastructureGrafanaDashboardManifest)); err != nil {
		return fmt.Errorf("[%s] writing Sophos infrastructure dashboard: %w", clusterName, err)
	}
	if err := runner.Run("kubectl apply -f /tmp/sophos-infrastructure-dashboard.yaml", out); err != nil {
		return fmt.Errorf("[%s] applying Sophos infrastructure dashboard: %w", clusterName, err)
	}
	ui.Success(out, "[%s] Sophos Grafana dashboards ready", clusterName)
	return nil
}
