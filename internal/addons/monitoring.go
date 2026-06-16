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
  plugins:
    - volkovlabs-echarts-panel
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
    - nodes=[cpu-usage,memory-usage,disk-bandwidth,network-bandwidth]
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
              "expr": "sum(rate(istio_requests_total{reporter=\"destination\",destination_workload_namespace=~\"$namespace\",source_workload=\"istio-gateway-istio\",destination_workload=\"frontend\"}[1m]) * on(destination_workload_namespace,destination_workload) group_left(label_group) label_replace(label_replace(kube_deployment_labels{namespace=~\"$namespace\",label_group=~\"$group\"} @ end(), \"destination_workload\", \"$1\", \"deployment\", \"(.*)\"), \"destination_workload_namespace\", \"$1\", \"namespace\", \"(.*)\"))"
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
              "expr": "sum(rate(istio_requests_total{reporter=\"destination\",destination_workload_namespace=~\"$namespace\",source_workload=\"istio-gateway-istio\",destination_workload=\"frontend\",response_code!~\"2..|3..\"}[1m]) * on(destination_workload_namespace,destination_workload) group_left(label_group) label_replace(label_replace(kube_deployment_labels{namespace=~\"$namespace\",label_group=~\"$group\"} @ end(), \"destination_workload\", \"$1\", \"deployment\", \"(.*)\"), \"destination_workload_namespace\", \"$1\", \"namespace\", \"(.*)\"))"
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
              "expr": "histogram_quantile(0.95, sum by (le) (rate(istio_request_duration_milliseconds_bucket{reporter=\"destination\",destination_workload_namespace=~\"$namespace\",source_workload=\"istio-gateway-istio\",destination_workload=\"frontend\"}[$p95_window]) * on(destination_workload_namespace,destination_workload) group_left(label_group) label_replace(label_replace(kube_deployment_labels{namespace=~\"$namespace\",label_group=~\"$group\"} @ end(), \"destination_workload\", \"$1\", \"deployment\", \"(.*)\"), \"destination_workload_namespace\", \"$1\", \"namespace\", \"(.*)\")))"
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
      "version": 1,
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
          "id": 1,
          "title": "Node Latency Graph",
          "type": "volkovlabs-echarts-panel",
          "gridPos": {
            "h": 16,
            "w": 24,
            "x": 0,
            "y": 0
          },
          "targets": [
            {
              "refId": "A",
              "instant": true,
              "expr": "1000 * sum by (origin_node, destination_node) (rate(node_latency_sum{origin_node=~\"$origin_node\",destination_node=~\"$destination_node\"}[$latency_window])) / sum by (origin_node, destination_node) (rate(node_latency_count{origin_node=~\"$origin_node\",destination_node=~\"$destination_node\"}[$latency_window]))"
            },
            {
              "refId": "B",
              "instant": true,
              "expr": "kube_node_annotations{node=~\"$node\"}"
            }
          ],
          "fieldConfig": {
            "defaults": {},
            "overrides": []
          },
          "options": {
            "renderer": "canvas",
            "editorMode": "code",
            "getOption": "const valueAt = (values, index) => values.get ? values.get(index) : values[index];\nconst finiteNumber = (value) => {\n  const next = Number(value);\n  return Number.isFinite(next) ? next : null;\n};\nconst formatBytes = (value) => {\n  if (!Number.isFinite(value)) {\n    return 'n/a';\n  }\n  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];\n  let next = value;\n  let unit = 0;\n  while (Math.abs(next) >= 1024 && unit < units.length - 1) {\n    next /= 1024;\n    unit += 1;\n  }\n  return next.toFixed(unit === 0 ? 0 : 1) + ' ' + units[unit];\n};\nconst formatRate = (value) => Number.isFinite(value) ? formatBytes(value) + '/s' : 'n/a';\nconst formatMillicores = (value) => Number.isFinite(value) ? value.toFixed(1) + ' mCPU' : 'n/a';\nconst links = [];\nconst nodeNames = new Set();\nconst nodeMetrics = {};\nconst storageKey = 'sophos-node-latency-positions:v1:' + (context.panel.id || 'infrastructure');\nconst chartWidth = context.panel.chart && context.panel.chart.getWidth ? context.panel.chart.getWidth() : 900;\nconst chartHeight = context.panel.chart && context.panel.chart.getHeight ? context.panel.chart.getHeight() : 620;\nconst loadPositions = () => {\n  try {\n    return JSON.parse(localStorage.getItem(storageKey) || '{}');\n  } catch (error) {\n    return {};\n  }\n};\nconst savePositions = (positions) => {\n  try {\n    localStorage.setItem(storageKey, JSON.stringify(positions));\n  } catch (error) {}\n};\nconst capturePositions = () => {\n  const positions = loadPositions();\n  const chart = context.panel.chart;\n  const model = chart && chart.getModel && chart.getModel();\n  const seriesModel = model && model.getSeriesByIndex && model.getSeriesByIndex(0);\n  const data = seriesModel && seriesModel.getData && seriesModel.getData();\n  if (!data || !data.each) {\n    return positions;\n  }\n  data.each((idx) => {\n    const raw = data.getRawDataItem(idx) || {};\n    const layout = data.getItemLayout(idx);\n    const name = raw.name || raw.id;\n    if (name && Array.isArray(layout) && Number.isFinite(layout[0]) && Number.isFinite(layout[1])) {\n      positions[name] = {x: layout[0], y: layout[1], width: chartWidth, height: chartHeight};\n    }\n  });\n  savePositions(positions);\n  return positions;\n};\nconst storedPositions = context.panel.chart ? capturePositions() : loadPositions();\n\ncontext.panel.data.series.forEach((frame) => {\n  const valueField = frame.fields.find((field) => field.type === 'number');\n  if (!valueField) {\n    return;\n  }\n\n  const labels = valueField.labels || {};\n  const source = labels.origin_node;\n  const target = labels.destination_node;\n  if (source && target) {\n    const values = valueField.values;\n    const latency = Number(valueAt(values, values.length - 1));\n    if (!Number.isFinite(latency)) {\n      return;\n    }\n    nodeNames.add(source);\n    nodeNames.add(target);\n    links.push({\n      source,\n      target,\n      value: latency,\n      latencyLabel: latency.toFixed(3) + ' ms',\n    });\n    return;\n  }\n\n  const node = labels.node;\n  if (node) {\n    nodeNames.add(node);\n    nodeMetrics[node] = {\n      cpu: finiteNumber(labels.annotation_cpu_usage),\n      memory: finiteNumber(labels.annotation_memory_usage),\n      disk: finiteNumber(labels.annotation_disk_bandwidth),\n      network: finiteNumber(labels.annotation_network_bandwidth),\n    };\n  }\n});\n\nconst width = Math.max(chartWidth, 480);\nconst height = Math.max(chartHeight, 360);\nconst centerX = width / 2;\nconst centerY = height / 2;\nconst radius = Math.min(width, height) * 0.36;\nconst names = Array.from(nodeNames).sort();\nconst nodes = names.map((name, index) => {\n  const angle = names.length === 1 ? 0 : (2 * Math.PI * index) / names.length - Math.PI / 2;\n  const saved = storedPositions[name];\n  const savedWidth = saved && Number.isFinite(saved.width) ? saved.width : 900;\n  const savedHeight = saved && Number.isFinite(saved.height) ? saved.height : 420;\n  const savedX = saved && Number.isFinite(saved.x) ? (saved.x / savedWidth) * width : null;\n  const savedY = saved && Number.isFinite(saved.y) ? (saved.y / savedHeight) * height : null;\n  const metrics = nodeMetrics[name] || {};\n  const labelParts = [name];\n  if (Number.isFinite(metrics.cpu)) {\n    labelParts.push(metrics.cpu.toFixed(1) + ' mCPU');\n  }\n  if (Number.isFinite(metrics.memory)) {\n    labelParts.push(formatBytes(metrics.memory));\n  }\n  return {\n    id: name,\n    name,\n    metrics,\n    x: Number.isFinite(savedX) ? savedX : centerX + radius * Math.cos(angle),\n    y: Number.isFinite(savedY) ? savedY : centerY + radius * Math.sin(angle),\n    symbolSize: 72,\n    draggable: true,\n    label: {show: true, formatter: labelParts.join('\\n')},\n  };\n});\n\nif (context.panel.chart) {\n  if (context.panel.chart.__sophosSaveNodePositions) {\n    context.panel.chart.off('mouseup', context.panel.chart.__sophosSaveNodePositions);\n    context.panel.chart.off('globalout', context.panel.chart.__sophosSaveNodePositions);\n  }\n  context.panel.chart.__sophosSaveNodePositions = () => capturePositions();\n  context.panel.chart.on('mouseup', context.panel.chart.__sophosSaveNodePositions);\n  context.panel.chart.on('globalout', context.panel.chart.__sophosSaveNodePositions);\n}\n\nreturn {\n  backgroundColor: 'transparent',\n  animation: false,\n  tooltip: {\n    formatter: (params) => {\n      if (params.dataType === 'edge') {\n        return params.data.source + ' -> ' + params.data.target + '<br/>' + params.data.latencyLabel;\n      }\n      const metrics = params.data.metrics || {};\n      return [\n        '<strong>' + params.data.name + '</strong>',\n        'CPU: ' + formatMillicores(metrics.cpu),\n        'Memory: ' + formatBytes(metrics.memory),\n        'Disk bandwidth: ' + formatRate(metrics.disk),\n        'Network bandwidth: ' + formatRate(metrics.network),\n      ].join('<br/>');\n    },\n  },\n  series: [\n    {\n      type: 'graph',\n      layout: 'none',\n      coordinateSystem: null,\n      data: nodes,\n      links,\n      roam: true,\n      draggable: true,\n      edgeSymbol: ['none', 'arrow'],\n      edgeSymbolSize: [0, 10],\n      edgeLabel: {\n        show: true,\n        position: 'middle',\n        formatter: (params) => params.data.latencyLabel,\n        fontSize: 12,\n      },\n      label: {show: true, position: 'inside', fontWeight: 600, fontSize: 11, lineHeight: 14},\n      lineStyle: {width: 1.8, curveness: 0.18, opacity: 0.75},\n      emphasis: {focus: 'adjacency'},\n    },\n  ],\n};"
          }
        },
        {
          "id": 2,
          "title": "Node Latency Mean Matrix",
          "type": "table",
          "gridPos": {
            "h": 8,
            "w": 24,
            "x": 0,
            "y": 16
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
            "y": 24
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
            "y": 24
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
          "id": 5,
          "title": "Node CPU Usage - Cores",
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
            "y": 32
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
            "y": 40
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
            "y": 40
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
            "y": 48
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
            "y": 48
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
            "y": 56
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
            "y": 56
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
            "y": 64
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
