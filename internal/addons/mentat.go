package addons

import (
	"fmt"
	"io"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

// mentatCoreManifest deploys the ServiceAccount, RBAC, and DaemonSet.
// Format args: version (string), sleepSeconds (int).
const mentatCoreManifest = `apiVersion: v1
kind: ServiceAccount
metadata:
  name: mentat
  namespace: observability
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: mentat
rules:
  - apiGroups: [""]
    resources: ["nodes"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: mentat
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: mentat
subjects:
  - kind: ServiceAccount
    name: mentat
    namespace: observability
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: mentat
  namespace: observability
  labels:
    app: mentat
spec:
  selector:
    matchLabels:
      app: mentat
  template:
    metadata:
      labels:
        app: mentat
    spec:
      serviceAccountName: mentat
      tolerations:
      - key: "ManagementOnly"
        operator: "Equal"
        value: "true"
        effect: "NoSchedule"
      containers:
      - name: mentat
        image: ghcr.io/unict-cclab/mentat:%s
        ports:
        - name: metrics
          containerPort: 2112
        env:
        - name: SLEEP_SECONDS
          value: "%d"
        - name: NODE_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
`

const mentatPodMonitor = `apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: mentat
  namespace: observability
  labels:
    monitoring: mentat
    release: prometheus-stack
spec:
  selector:
    matchLabels:
      app: mentat
  podMetricsEndpoints:
  - port: metrics
`

// InstallMentat deploys the mentat network-latency DaemonSet into the observability
// namespace. When withMonitoring is true the PodMonitor is also applied so that
// Prometheus scrapes the latency metrics automatically.
func InstallMentat(runner *util.Runner, addon config.MentatConfig, clusterName string, withMonitoring bool, out io.Writer) error {
	ui.Step(out, "[%s] installing mentat %s (probe interval %ds)...", clusterName, addon.Version, addon.SleepSeconds)

	core := fmt.Sprintf(mentatCoreManifest, addon.Version, addon.SleepSeconds)
	if err := runner.WriteFile("/tmp/mentat.yaml", []byte(core)); err != nil {
		return fmt.Errorf("[%s] writing mentat manifest: %w", clusterName, err)
	}
	if err := runner.Run("kubectl apply -f /tmp/mentat.yaml", out); err != nil {
		return fmt.Errorf("[%s] applying mentat manifest: %w", clusterName, err)
	}

	if withMonitoring {
		if err := runner.WriteFile("/tmp/mentat-podmonitor.yaml", []byte(mentatPodMonitor)); err != nil {
			return fmt.Errorf("[%s] writing mentat PodMonitor: %w", clusterName, err)
		}
		if err := runner.Run("kubectl apply -f /tmp/mentat-podmonitor.yaml", out); err != nil {
			return fmt.Errorf("[%s] applying mentat PodMonitor: %w", clusterName, err)
		}
	}

	ui.Success(out, "[%s] mentat ready", clusterName)
	return nil
}
