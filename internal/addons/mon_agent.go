package addons

import (
	"fmt"
	"io"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

// Format args: version, prometheusURL, scrapePeriodSeconds, promQLRange, namespaceSelector.
const monAgentManifestTemplate = `apiVersion: v1
kind: Namespace
metadata:
  name: observability
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: mon-agent
  namespace: observability
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: mon-agent
rules:
  - apiGroups: [""]
    resources: ["namespaces", "nodes"]
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources: ["nodes"]
    verbs: ["patch"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get", "list", "watch", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: mon-agent
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: mon-agent
subjects:
  - kind: ServiceAccount
    name: mon-agent
    namespace: observability
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mon-agent
  namespace: observability
  labels:
    app: mon-agent
spec:
  replicas: 1
  selector:
    matchLabels:
      app: mon-agent
  template:
    metadata:
      labels:
        app: mon-agent
    spec:
      serviceAccountName: mon-agent
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
      containers:
        - name: mon-agent
          image: ghcr.io/unict-cclab/mon-agent:%s
          imagePullPolicy: IfNotPresent
          env:
            - name: PROMETHEUS_URL
              value: %q
            - name: SCRAPE_PERIOD_SECONDS
              value: "%d"
            - name: PROMQL_RANGE
              value: %q
            - name: NAMESPACE_LABEL_SELECTOR
              value: %q
`

func InstallMonAgent(runner *util.Runner, addon config.MonAgentConfig, clusterName string, out io.Writer) error {
	ui.Step(out, "[%s] installing mon-agent %s...", clusterName, addon.Version)
	manifest := fmt.Sprintf(
		monAgentManifestTemplate,
		addon.Version,
		addon.PrometheusURL,
		addon.ScrapePeriodSeconds,
		addon.PromQLRange,
		addon.NamespaceLabelSelector,
	)
	if err := runner.WriteFile("/tmp/mon-agent.yaml", []byte(manifest)); err != nil {
		return fmt.Errorf("[%s] writing mon-agent manifest: %w", clusterName, err)
	}
	if err := runner.Run("kubectl apply -f /tmp/mon-agent.yaml", out); err != nil {
		return fmt.Errorf("[%s] applying mon-agent manifest: %w", clusterName, err)
	}
	ui.Success(out, "[%s] mon-agent ready", clusterName)
	return nil
}
