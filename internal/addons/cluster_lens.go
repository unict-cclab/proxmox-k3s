package addons

import (
	"fmt"
	"io"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

// Format args: version, refresh, nodePort.
const clusterLensManifestTemplate = `apiVersion: v1
kind: Namespace
metadata:
  name: observability
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: cluster-lens
  namespace: observability
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: cluster-lens
rules:
  - apiGroups: [""]
    resources: ["nodes", "pods"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["apps"]
    resources: ["deployments", "replicasets"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: cluster-lens
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cluster-lens
subjects:
  - kind: ServiceAccount
    name: cluster-lens
    namespace: observability
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cluster-lens
  namespace: observability
  labels:
    app: cluster-lens
spec:
  replicas: 1
  selector:
    matchLabels:
      app: cluster-lens
  template:
    metadata:
      labels:
        app: cluster-lens
    spec:
      serviceAccountName: cluster-lens
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
        - name: cluster-lens
          image: ghcr.io/unict-cclab/cluster-lens:%s
          imagePullPolicy: IfNotPresent
          ports:
            - name: http
              containerPort: 8088
          env:
            - name: CLUSTER_LENS_ADDR
              value: :8088
            - name: CLUSTER_LENS_REFRESH
              value: %q
            - name: CLUSTER_LENS_CONTEXT
              value: in-cluster
---
apiVersion: v1
kind: Service
metadata:
  name: cluster-lens
  namespace: observability
  labels:
    app: cluster-lens
spec:
  type: NodePort
  selector:
    app: cluster-lens
  ports:
    - name: http
      port: 8088
      targetPort: http
      nodePort: %d
`

func InstallClusterLens(runner *util.Runner, addon config.ClusterLensConfig, clusterName string, out io.Writer) error {
	ui.Step(out, "[%s] installing cluster-lens %s (UI :%d)...", clusterName, addon.Version, addon.NodePort)
	manifest := fmt.Sprintf(clusterLensManifestTemplate, addon.Version, addon.Refresh, addon.NodePort)
	if err := runner.WriteFile("/tmp/cluster-lens.yaml", []byte(manifest)); err != nil {
		return fmt.Errorf("[%s] writing cluster-lens manifest: %w", clusterName, err)
	}
	if err := runner.Run("kubectl apply -f /tmp/cluster-lens.yaml", out); err != nil {
		return fmt.Errorf("[%s] applying cluster-lens manifest: %w", clusterName, err)
	}
	ui.Success(out, "[%s] cluster-lens ready — UI :%d", clusterName, addon.NodePort)
	return nil
}
