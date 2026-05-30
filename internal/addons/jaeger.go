package addons

import (
	"fmt"
	"io"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

// jaegerManifestTemplate deploys Jaeger all-in-one into istio-system.
// Format args: nodePort (int), version (string).
const jaegerManifestTemplate = `apiVersion: v1
kind: Service
metadata:
  labels:
    service: jaeger
  name: jaeger
  namespace: observability
spec:
  type: NodePort
  ports:
    - name: admin
      port: 14269
      targetPort: 14269
    - name: config
      port: 5778
      targetPort: 5778
    - name: http-thrift
      port: 14268
      targetPort: 14268
    - name: binary-thrift
      port: 14267
      targetPort: 14267
    - name: otlp-grpc
      port: 4317
      targetPort: 4317
    - name: otlp-http
      port: 4318
      targetPort: 4318
    - name: ui
      port: 16686
      targetPort: 16686
      nodePort: %d
    - name: zipkin-compact
      port: 5775
      protocol: UDP
      targetPort: 5775
    - name: jaeger-compact
      port: 6831
      protocol: UDP
      targetPort: 6831
    - name: jaeger-binary
      port: 6832
      protocol: UDP
      targetPort: 6832
  selector:
    service: jaeger
---
apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    service: jaeger
  name: jaeger
  namespace: observability
spec:
  replicas: 1
  selector:
    matchLabels:
      service: jaeger
  strategy: {}
  template:
    metadata:
      labels:
        service: jaeger
    spec:
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
      containers:
      - image: jaegertracing/all-in-one:%s
        name: jaeger
        ports:
        - containerPort: 14269
        - containerPort: 5778
        - containerPort: 14268
        - containerPort: 14267
        - containerPort: 4317
        - containerPort: 4318
        - containerPort: 16686
        - containerPort: 5775
          protocol: UDP
        - containerPort: 6831
          protocol: UDP
        - containerPort: 6832
          protocol: UDP
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 1000m
            memory: 1Gi
      restartPolicy: Always
`

// InstallJaeger deploys Jaeger all-in-one into the istio-system namespace.
func InstallJaeger(runner *util.Runner, addon config.JaegerConfig, clusterName string, out io.Writer) error {
	ui.Step(out, "[%s] deploying Jaeger %s (UI :%d)...", clusterName, addon.Version, addon.NodePort)
	manifest := fmt.Sprintf(jaegerManifestTemplate, addon.NodePort, addon.Version)
	if err := runner.WriteFile("/tmp/jaeger.yaml", []byte(manifest)); err != nil {
		return fmt.Errorf("[%s] writing Jaeger manifest: %w", clusterName, err)
	}
	if err := runner.Run("kubectl apply -f /tmp/jaeger.yaml", out); err != nil {
		return fmt.Errorf("[%s] applying Jaeger manifest: %w", clusterName, err)
	}
	ui.Success(out, "[%s] Jaeger ready — UI :%d", clusterName, addon.NodePort)
	return nil
}
