package addons

import (
	"fmt"
	"io"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

// kialiTracingDisabled is the tracing block for Kiali's ConfigMap when Jaeger is absent.
const kialiTracingDisabled = `      tracing:
        enabled: false`

// kialiTracingEnabled is the tracing block for Kiali's ConfigMap when Jaeger is deployed.
const kialiTracingEnabled = `      tracing:
        enabled: true
        in_cluster_url: "http://jaeger.observability:16686"
        use_grpc: false`

// kialiManifestTemplate is the full Kiali manifest (ServiceAccount, ConfigMap, ClusterRole,
// ClusterRoleBinding, Service, Deployment) deployed into istio-system.
// Format args: tracingBlock (string), signingKey (string), nodePort (int), imageVersion (string).
const kialiManifestTemplate = `apiVersion: v1
kind: ServiceAccount
metadata:
  name: kiali
  namespace: istio-system
  labels:
    app: kiali
    app.kubernetes.io/name: kiali
    app.kubernetes.io/instance: kiali
    app.kubernetes.io/part-of: kiali
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: kiali
  namespace: istio-system
  labels:
    app: kiali
    app.kubernetes.io/name: kiali
    app.kubernetes.io/instance: kiali
    app.kubernetes.io/part-of: kiali
data:
  config.yaml: |
    additional_display_details:
    - annotation: kiali.io/api-spec
      icon_annotation: kiali.io/api-type
      title: API Documentation
    auth:
      openid: {}
      openshift:
        client_id_prefix: kiali
      strategy: anonymous
    clustering:
      autodetect_secrets:
        enabled: true
        label: kiali.io/multiCluster=true
      clusters: []
    deployment:
      additional_service_yaml: {}
      affinity:
        node: {}
        pod: {}
        pod_anti: {}
      cluster_wide_access: true
      image_name: quay.io/kiali/kiali
      image_pull_policy: IfNotPresent
      ingress_enabled: false
      instance_name: kiali
      logger:
        log_format: text
        log_level: info
        sampler_rate: "1"
        time_field_format: 2006-01-02T15:04:05Z07:00
      namespace: istio-system
      pod_labels:
        sidecar.istio.io/inject: "false"
      replicas: 1
      resources:
        limits:
          memory: 1Gi
        requests:
          cpu: 10m
          memory: 64Mi
      secret_name: kiali
    external_services:
      custom_dashboards:
        enabled: true
      istio:
        root_namespace: istio-system
      prometheus:
        url: "http://prometheus-stack-kube-prom-prometheus.observability:9090/"
%s
    identity:
      cert_file: ""
      private_key_file: ""
    istio_namespace: istio-system
    kiali_feature_flags:
      disabled_features: []
      validations:
        ignore:
        - KIA1301
    login_token:
      signing_key: %s
    server:
      observability:
        metrics:
          enabled: true
          port: 9090
      port: 20001
      web_root: /kiali
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kiali
  labels:
    app: kiali
    app.kubernetes.io/name: kiali
    app.kubernetes.io/instance: kiali
    app.kubernetes.io/part-of: kiali
rules:
- apiGroups: [""]
  resources:
  - configmaps
  - endpoints
  - pods/log
  verbs:
  - get
  - list
  - watch
- apiGroups: [""]
  resources:
  - namespaces
  - pods
  - replicationcontrollers
  - services
  verbs:
  - get
  - list
  - watch
  - patch
- apiGroups: [""]
  resources:
  - pods/portforward
  verbs:
  - create
- apiGroups: ["extensions", "apps"]
  resources:
  - daemonsets
  - deployments
  - replicasets
  - statefulsets
  verbs:
  - get
  - list
  - watch
  - patch
- apiGroups: ["batch"]
  resources:
  - cronjobs
  - jobs
  verbs:
  - get
  - list
  - watch
  - patch
- apiGroups:
  - networking.istio.io
  - security.istio.io
  - extensions.istio.io
  - telemetry.istio.io
  - gateway.networking.k8s.io
  resources: ["*"]
  verbs:
  - get
  - list
  - watch
  - create
  - delete
  - patch
- apiGroups: ["authentication.k8s.io"]
  resources:
  - tokenreviews
  verbs:
  - create
- apiGroups: ["admissionregistration.k8s.io"]
  resources:
  - mutatingwebhookconfigurations
  verbs:
  - get
  - list
  - watch
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kiali
  labels:
    app: kiali
    app.kubernetes.io/name: kiali
    app.kubernetes.io/instance: kiali
    app.kubernetes.io/part-of: kiali
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kiali
subjects:
- kind: ServiceAccount
  name: kiali
  namespace: istio-system
---
apiVersion: v1
kind: Service
metadata:
  name: kiali
  namespace: istio-system
  labels:
    app: kiali
    app.kubernetes.io/name: kiali
    app.kubernetes.io/instance: kiali
    app.kubernetes.io/part-of: kiali
spec:
  type: NodePort
  ports:
  - name: http
    appProtocol: http
    protocol: TCP
    port: 20001
    nodePort: %d
  - name: http-metrics
    appProtocol: http
    protocol: TCP
    port: 9090
  selector:
    app.kubernetes.io/name: kiali
    app.kubernetes.io/instance: kiali
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kiali
  namespace: istio-system
  labels:
    app: kiali
    app.kubernetes.io/name: kiali
    app.kubernetes.io/instance: kiali
    app.kubernetes.io/part-of: kiali
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: kiali
      app.kubernetes.io/instance: kiali
  strategy:
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 1
    type: RollingUpdate
  template:
    metadata:
      name: kiali
      labels:
        app: kiali
        app.kubernetes.io/name: kiali
        app.kubernetes.io/instance: kiali
        app.kubernetes.io/part-of: kiali
        sidecar.istio.io/inject: "false"
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "9090"
        kiali.io/dashboards: go,kiali
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
      serviceAccountName: kiali
      containers:
      - image: "quay.io/kiali/kiali:%s"
        imagePullPolicy: IfNotPresent
        name: kiali
        command:
        - "/opt/kiali/kiali"
        - "-config"
        - "/kiali-configuration/config.yaml"
        terminationMessagePolicy: FallbackToLogsOnError
        securityContext:
          allowPrivilegeEscalation: false
          privileged: false
          readOnlyRootFilesystem: true
          runAsNonRoot: true
          capabilities:
            drop:
            - ALL
        ports:
        - name: api-port
          containerPort: 20001
        - name: http-metrics
          containerPort: 9090
        readinessProbe:
          httpGet:
            path: /kiali/healthz
            port: api-port
            scheme: HTTP
          initialDelaySeconds: 5
          periodSeconds: 30
        livenessProbe:
          httpGet:
            path: /kiali/healthz
            port: api-port
            scheme: HTTP
          initialDelaySeconds: 5
          periodSeconds: 30
        startupProbe:
          httpGet:
            path: /kiali/healthz
            port: api-port
            scheme: HTTP
          failureThreshold: 6
          initialDelaySeconds: 30
          periodSeconds: 10
        env:
        - name: ACTIVE_NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
        - name: LOG_LEVEL
          value: "info"
        - name: LOG_FORMAT
          value: "text"
        - name: LOG_TIME_FIELD_FORMAT
          value: "2006-01-02T15:04:05Z07:00"
        - name: LOG_SAMPLER_RATE
          value: "1"
        volumeMounts:
        - name: kiali-configuration
          mountPath: "/kiali-configuration"
        - name: kiali-cert
          mountPath: "/kiali-cert"
        - name: kiali-secret
          mountPath: "/kiali-secret"
        - name: kiali-cabundle
          mountPath: "/kiali-cabundle"
        resources:
          limits:
            cpu: 1000m
            memory: 1Gi
          requests:
            cpu: 10m
            memory: 64Mi
      volumes:
      - name: kiali-configuration
        configMap:
          name: kiali
      - name: kiali-cert
        secret:
          secretName: istio.kiali-service-account
          optional: true
      - name: kiali-secret
        secret:
          secretName: kiali
          optional: true
      - name: kiali-cabundle
        configMap:
          name: kiali-cabundle
          optional: true
`

// InstallKiali deploys Kiali into the istio-system namespace.
// jaegerEnabled controls whether the tracing integration is wired to Jaeger.
func InstallKiali(runner *util.Runner, addon config.KialiConfig, jaegerEnabled bool, clusterName string, out io.Writer) error {
	ui.Step(out, "[%s] deploying Kiali %s (UI :%d)...", clusterName, addon.Version, addon.NodePort)

	tracingBlock := kialiTracingDisabled
	if jaegerEnabled {
		tracingBlock = kialiTracingEnabled
	}

	manifest := fmt.Sprintf(kialiManifestTemplate, tracingBlock, addon.SigningKey, addon.NodePort, addon.Version)
	if err := runner.WriteFile("/tmp/kiali.yaml", []byte(manifest)); err != nil {
		return fmt.Errorf("[%s] writing Kiali manifest: %w", clusterName, err)
	}
	if err := runner.Run("kubectl apply -f /tmp/kiali.yaml", out); err != nil {
		return fmt.Errorf("[%s] applying Kiali manifest: %w", clusterName, err)
	}
	ui.Success(out, "[%s] Kiali ready — UI :%d", clusterName, addon.NodePort)
	return nil
}
