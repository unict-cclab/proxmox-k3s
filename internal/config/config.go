package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the unified configuration type for both single-cluster and
// multi-cluster deployments. When Clusters is non-empty the file is treated
// as a multi-cluster config and the top-level ClusterName / KubeconfigPath /
// K3s / ControlPlane / Workers fields are ignored.
type Config struct {
	// Single-cluster identity (ignored in multi-cluster mode)
	ClusterName    string `yaml:"cluster_name"`
	KubeconfigPath string `yaml:"kubeconfig_path"`

	// SSHKeyPath is the path to the SSH private key used for all VMs.
	// If empty the key is auto-generated and stored under
	// ~/.proxmox-k3s/<cluster-name>/id_ed25519.
	SSHKeyPath string `yaml:"ssh_key_path"`

	// Shared across all modes
	Proxmox  ProxmoxConfig  `yaml:"proxmox"`
	Template TemplateConfig `yaml:"template"`

	// templateConfigured is true when the YAML contained an explicit template block.
	templateConfigured bool

	// Single-cluster k3s and node definitions
	K3s          K3sConfig    `yaml:"k3s"`
	PodCIDR      string       `yaml:"pod_cidr"`
	ServiceCIDR  string       `yaml:"service_cidr"`
	ControlPlane []CPNode     `yaml:"control_plane"`
	Workers      []WorkerNode `yaml:"workers"`

	// Multi-cluster fields; presence of Clusters activates multi-cluster mode
	Clusters    []ClusterSpec      `yaml:"clusters"`
	ClusterMesh []ClusterMeshEntry `yaml:"cluster_mesh"`
	Registry    *RegistryConfig    `yaml:"registry,omitempty"`
	NFS         *NFSConfig         `yaml:"nfs,omitempty"`
}

// IsMultiCluster reports whether the config defines multiple clusters.
func (c *Config) IsMultiCluster() bool { return len(c.Clusters) > 0 }

// ClusterMeshID returns the Cilium cluster ID for the named cluster and whether
// it is part of the cluster mesh.
func (c *Config) ClusterMeshID(name string) (int, bool) {
	for _, e := range c.ClusterMesh {
		if e.Cluster == name {
			return e.ID, true
		}
	}
	return 0, false
}

// ClusterSpec is the per-cluster definition used in multi-cluster configs.
type ClusterSpec struct {
	Name           string        `yaml:"name"`
	KubeconfigPath string        `yaml:"kubeconfig_path"`
	PodCIDR        string        `yaml:"pod_cidr"`
	ServiceCIDR    string        `yaml:"service_cidr"`
	Addons         ClusterAddons `yaml:"addons"`
	PVCs           []PVCConfig   `yaml:"pvcs,omitempty"`
	K3s            K3sConfig     `yaml:"k3s"`
	ControlPlane   []CPNode      `yaml:"control_plane"`
	Workers        []WorkerNode  `yaml:"workers"`
}

// PVCConfig describes a PersistentVolumeClaim created after cluster addons.
type PVCConfig struct {
	Name         string `yaml:"name"`
	Namespace    string `yaml:"namespace"`
	StorageClass string `yaml:"storageClass"`
	Size         string `yaml:"size"`
}

// ClusterAddons groups optional per-cluster addon configurations.
type ClusterAddons struct {
	Cilium      CiliumConfig        `yaml:"cilium"`
	Monitoring  MonitoringConfig    `yaml:"monitoring"`
	MonAgent    MonAgentConfig      `yaml:"mon_agent"`
	ClusterLens ClusterLensConfig   `yaml:"cluster_lens"`
	Logging     LoggingConfig       `yaml:"logging"`
	Istio       IstioConfig         `yaml:"istio"`
	Jaeger      JaegerConfig        `yaml:"jaeger"`
	Kiali       KialiConfig         `yaml:"kiali"`
	NFS         NFSAddonConfig      `yaml:"nfs"`
	ChaosMesh   ChaosMeshConfig     `yaml:"chaos_mesh"`
	CPAOperator CPAOperatorConfig   `yaml:"custom_pod_autoscaler"`
	Mentat      MentatConfig        `yaml:"mentat"`
	Registry    *ClusterRegistryRef `yaml:"registry,omitempty"`
}

// ClusterRegistryRef references a pre-existing Harbor registry for a cluster.
// Only the connection details are needed — no VM provisioning is performed.
type ClusterRegistryRef struct {
	Hostname string `yaml:"hostname"`
	HTTPPort int    `yaml:"http_port"`
}

// CiliumConfig configures Cilium CNI installation per cluster.
// Set enabled: true to install Cilium. Required for cluster_mesh participation.
type CiliumConfig struct {
	Enabled          bool   `yaml:"enabled"`
	Version          string `yaml:"version"`
	HubbleUINodePort int    `yaml:"hubble_ui_node_port"` // NodePort for Hubble UI, default 32080
}

// MonitoringConfig configures the kube-prometheus-stack installation.
// Set enabled: true to install the monitoring stack.
type MonitoringConfig struct {
	Enabled              bool   `yaml:"enabled"`
	Version              string `yaml:"version"`
	PrometheusNodePort   int    `yaml:"prometheus_node_port"`
	GrafanaNodePort      int    `yaml:"grafana_node_port"`
	GrafanaAdminPassword string `yaml:"grafana_admin_password"`
}

// MonAgentConfig configures mon-agent, which annotates nodes and deployments
// with metrics collected from Prometheus.
type MonAgentConfig struct {
	Enabled                bool   `yaml:"enabled"`
	Version                string `yaml:"version"` // image tag, default "v0.0.6"
	PrometheusURL          string `yaml:"prometheus_url"`
	ScrapePeriodSeconds    int    `yaml:"scrape_period_seconds"`
	PromQLRange            string `yaml:"promql_range"`
	NamespaceLabelSelector string `yaml:"namespace_label_selector"`
}

// ClusterLensConfig configures the cluster-lens topology UI.
type ClusterLensConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Version  string `yaml:"version"`   // image tag, default "v0.0.5"
	NodePort int    `yaml:"node_port"` // NodePort for UI (port 8088), default 32088
	Refresh  string `yaml:"refresh"`   // frontend polling interval, default "2s"
}

// LoggingConfig configures Loki and Alloy for Kubernetes event logging.
// Set enabled: true to collect HPA scaling events and expose Loki via NodePort.
type LoggingConfig struct {
	Enabled      bool   `yaml:"enabled"`
	LokiVersion  string `yaml:"loki_version"`   // Helm chart version, default "6.41.1"
	AlloyVersion string `yaml:"alloy_version"`  // Helm chart version, default "1.4.0"
	LokiNodePort int    `yaml:"loki_node_port"` // NodePort for Loki gateway/API, default 32099
	Retention    string `yaml:"retention"`      // Loki retention period, default "30d"
	StorageSize  string `yaml:"storage_size"`   // Loki PVC size, default "10Gi"
}

// IstioConfig configures Istio service mesh installation per cluster.
// Set enabled: true to install Istio (base CRDs + istiod). Disabled by default.
type IstioConfig struct {
	Enabled           bool   `yaml:"enabled"`
	Version           string `yaml:"version"`
	GatewayAPIVersion string `yaml:"gateway_api_version"`
}

// JaegerConfig configures Jaeger all-in-one tracing deployment per cluster.
// Requires Istio to be enabled. Disabled by default.
type JaegerConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Version      string `yaml:"version"`       // image tag, default "1.53"
	NodePort     int    `yaml:"node_port"`     // NodePort for UI (port 16686), default 30002
	StorageClass string `yaml:"storage_class"` // PVC StorageClass for Badger data, default "local-path"
	StorageSize  string `yaml:"storage_size"`  // Badger PVC size, default "10Gi"
}

// KialiConfig configures Kiali service mesh console deployment per cluster.
// Installed automatically when Istio is enabled; configure node_port/version here.
type KialiConfig struct {
	Version    string `yaml:"version"`     // image tag, default "v2.8"
	NodePort   int    `yaml:"node_port"`   // NodePort for UI (port 20001), default 30001
	SigningKey string `yaml:"signing_key"` // JWT signing key, default "proxmox-k3s-kiali"
}

// ClusterMeshEntry assigns a Cilium cluster ID to a named cluster that
// participates in the cluster mesh.
type ClusterMeshEntry struct {
	Cluster string `yaml:"cluster"`
	ID      int    `yaml:"id"`
}

// NFSConfig defines an optional NFS server VM that provides shared persistent
// storage for all clusters. Each cluster gets its own subdirectory export.
type NFSConfig struct {
	VM           CPNode `yaml:"vm"`
	DataDir      string `yaml:"data_dir"`
	ExportSubnet string `yaml:"export_subnet"`
}

// ChaosMeshConfig configures Chaos Mesh installation per cluster.
// Set enabled: true to install Chaos Mesh for fault injection testing.
type ChaosMeshConfig struct {
	Enabled           bool   `yaml:"enabled"`
	Version           string `yaml:"version"`
	DashboardNodePort int    `yaml:"dashboard_node_port"` // NodePort for the dashboard (port 2333), default 32300
}

// CPAOperatorConfig configures the Custom Pod Autoscaler operator installation.
// Enabled defaults to true; set enabled: false to skip installing the operator.
type CPAOperatorConfig struct {
	Enabled   *bool  `yaml:"enabled"`
	Version   string `yaml:"version"`   // Helm chart/operator version, default "v1.4.2"
	Release   string `yaml:"release"`   // Helm release name, default "custom-pod-autoscaler-operator"
	Namespace string `yaml:"namespace"` // Helm release namespace, default "default"
}

func (c CPAOperatorConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// MentatConfig configures the mentat inter-node network measurement DaemonSet.
// Set enabled: true to deploy mentat into the observability namespace.
type MentatConfig struct {
	Enabled                  bool   `yaml:"enabled"`
	Version                  string `yaml:"version"`                    // image tag, default "v0.3.0"
	SleepSeconds             int    `yaml:"sleep_seconds"`              // ICMP probe interval, default 5
	PingAttempts             int    `yaml:"ping_attempts"`              // ICMP packets per peer, default 5
	PingTimeoutSeconds       int    `yaml:"ping_timeout_seconds"`       // per-packet timeout, default 1
	BandwidthPort            int    `yaml:"bandwidth_port"`             // host port for peer probes, default 2113
	BandwidthBytes           int    `yaml:"bandwidth_bytes"`            // bytes per bandwidth probe, default 256 KiB
	BandwidthIntervalSeconds int    `yaml:"bandwidth_interval_seconds"` // bandwidth probe interval, default 90
	BandwidthJitterSeconds   int    `yaml:"bandwidth_jitter_seconds"`   // random extra bandwidth delay, default 30
	BandwidthTimeoutSeconds  int    `yaml:"bandwidth_timeout_seconds"`  // per-probe timeout, default 30
}

// NFSAddonConfig controls the NFS CSI driver installation for a cluster.
// Server and DataDir point to a pre-existing NFS server; no VM is provisioned.
type NFSAddonConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Version      string `yaml:"version"`
	Server       string `yaml:"server"`        // NFS server IP or hostname
	DataDir      string `yaml:"data_dir"`      // base export path (default: /data/nfs)
	ExportSubnet string `yaml:"export_subnet"` // who can mount (default: *)
}

// RegistryConfig defines an optional Harbor VM that acts as a pull-through
// cache for all clusters, reducing external bandwidth.
type RegistryConfig struct {
	VM     CPNode       `yaml:"vm"`
	Harbor HarborConfig `yaml:"harbor"`
}

// HarborConfig holds Harbor-specific settings.
type HarborConfig struct {
	Hostname      string `yaml:"hostname"`
	AdminPassword string `yaml:"admin_password"`
	DataVolume    string `yaml:"data_volume"`
	HTTPPort      int    `yaml:"http_port"`
}

type ProxmoxConfig struct {
	APIURL      string `yaml:"api_url"`
	TokenID     string `yaml:"token_id"`
	TokenSecret string `yaml:"token_secret"`
	InsecureTLS bool   `yaml:"insecure_tls"`
}

type TemplateConfig struct {
	Name             string `yaml:"name"`
	ProxmoxNode      string `yaml:"proxmox_node"`
	Storage          string `yaml:"storage"`
	ImageStorage     string `yaml:"image_storage"`
	Bridge           string `yaml:"bridge"`
	IP               string `yaml:"ip"`
	Gateway          string `yaml:"gateway"`
	DNS              string `yaml:"dns"`
	SubnetMask       int    `yaml:"subnet_mask"`
	DiskSize         int    `yaml:"disk_size"`
	Password         string `yaml:"password"`
	TimeoutSeconds   int    `yaml:"timeout_seconds"`
	CloneParallelism int    `yaml:"clone_parallelism"`
	OS               string `yaml:"os"`
	CloudImageURL    string `yaml:"cloud_image_url"`
	VMIDBase         int    `yaml:"vmid_base"`
}

type K3sConfig struct {
	Version           string `yaml:"version"`
	ExtraServerArgs   string `yaml:"extra_server_args"`
	ExtraAgentArgs    string `yaml:"extra_agent_args"`
	TaintControlPlane *bool  `yaml:"taint_control_plane"`
}

type CPNode struct {
	Template    string `yaml:"template"` // template VM name to clone (overrides global template.name)
	Name        string `yaml:"name"`
	ProxmoxNode string `yaml:"proxmox_node"`
	Storage     string `yaml:"storage"`
	Bridge      string `yaml:"bridge"`
	Cores       int    `yaml:"cores"`
	Memory      int    `yaml:"memory"`
	DiskSize    int    `yaml:"disk_size"`
	IP          string `yaml:"ip"`
	Gateway     string `yaml:"gateway"`
	DNS         string `yaml:"dns"`
	SubnetMask  int    `yaml:"subnet_mask"`
}

type WorkerNode struct {
	CPNode `yaml:",inline"`
	Labels []string `yaml:"labels"`
	Taints []string `yaml:"taints"`
}

// VMSSHUser is the SSH username created by cloud-init on all provisioned VMs.
const VMSSHUser = "ubuntu"

var KnownOSImages = map[string]string{
	"ubuntu-24.04": "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img",
	"ubuntu-22.04": "https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img",
	"debian-12":    "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2",
}

// HasTemplateConfig reports whether the config file contained an explicit
// template block. Use this to decide whether teardown should delete the template.
func (c *Config) HasTemplateConfig() bool { return c.templateConfigured }

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Record whether the user provided a template block before defaults fill it in.
	cfg.templateConfigured = cfg.Template.Name != "" || cfg.Template.ProxmoxNode != "" || cfg.Template.Storage != ""

	cfg.applyDefaults()

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ToClusterConfig builds a full single-cluster Config from a ClusterSpec by
// merging shared Proxmox credentials and template settings with the spec.
func (c *Config) ToClusterConfig(spec ClusterSpec) *Config {
	cfg := &Config{
		ClusterName:    spec.Name,
		KubeconfigPath: spec.KubeconfigPath,
		SSHKeyPath:     c.SSHKeyPath,
		Proxmox:        c.Proxmox,
		Template:       c.Template,
		K3s:            spec.K3s,
		PodCIDR:        spec.PodCIDR,
		ServiceCIDR:    spec.ServiceCIDR,
		ControlPlane:   spec.ControlPlane,
		Workers:        spec.Workers,
		// Note: Cilium, Monitoring, Istio are on ClusterSpec; callers use the spec directly.
	}
	cfg.applyDefaults()
	return cfg
}

// SSHKeyFilePath returns the SSH private key path to use for all VMs.
// It returns the configured path if set, otherwise ~/.proxmox-k3s/id_ed25519.
func (c *Config) SSHKeyFilePath() (string, error) {
	if c.SSHKeyPath != "" {
		return c.SSHKeyPath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".proxmox-k3s", "id_ed25519"), nil
}

func (c *Config) applyDefaults() {
	// Determine the first CP node for template fallbacks.
	firstCP := firstCPNode(c)

	// Template defaults (shared in both modes)
	if c.Template.Name == "" {
		baseName := c.ClusterName
		if baseName == "" {
			baseName = "k3s"
		}
		c.Template.Name = baseName + "-tmpl"
	}
	if c.Template.ProxmoxNode == "" {
		c.Template.ProxmoxNode = firstCP.ProxmoxNode
	}
	if c.Template.Storage == "" {
		c.Template.Storage = coalesce(firstCP.Storage, "local-lvm")
	}
	if c.Template.ImageStorage == "" {
		c.Template.ImageStorage = c.Template.Storage
	}
	if c.Template.Bridge == "" {
		c.Template.Bridge = coalesce(firstCP.Bridge, "vmbr0")
	}
	if c.Template.Gateway != "" {
		if c.Template.SubnetMask == 0 {
			c.Template.SubnetMask = 24
		}
		if c.Template.DNS == "" {
			c.Template.DNS = "1.1.1.1"
		}
	}
	if c.Template.OS == "" {
		c.Template.OS = "ubuntu-24.04"
	}
	if c.Template.DiskSize == 0 {
		c.Template.DiskSize = 10
	}
	if c.Template.VMIDBase == 0 {
		c.Template.VMIDBase = 8000
	}
	if c.Template.TimeoutSeconds == 0 {
		c.Template.TimeoutSeconds = 1800
	}
	if c.Template.CloneParallelism == 0 {
		c.Template.CloneParallelism = 1
	}
	if c.Template.CloudImageURL == "" {
		if u, ok := KnownOSImages[c.Template.OS]; ok {
			c.Template.CloudImageURL = u
		}
	}

	if c.K3s.TaintControlPlane == nil {
		v := true
		c.K3s.TaintControlPlane = &v
	}

	if c.IsMultiCluster() {
		c.applyMultiDefaults()
		return
	}

	// Single-cluster defaults
	if c.ClusterName == "" {
		c.ClusterName = "k3s-cluster"
	}
	if c.KubeconfigPath == "" {
		c.KubeconfigPath = "./kubeconfig"
	}
	for i := range c.ControlPlane {
		applyNodeDefaults(&c.ControlPlane[i])
	}
	for i := range c.Workers {
		applyNodeDefaults(&c.Workers[i].CPNode)
	}
}

func (c *Config) applyMultiDefaults() {
	const (
		defaultCiliumVersion           = "1.19.4"
		defaultMonitoringVersion       = "84.5.0"
		defaultMonAgentVersion         = "v0.0.6"
		defaultClusterLensVersion      = "v0.0.5"
		defaultLokiVersion             = "6.41.1"
		defaultAlloyVersion            = "1.4.0"
		defaultIstioVersion            = "1.30.0"
		defaultJaegerVersion           = "1.53"
		defaultKialiVersion            = "v2.8"
		defaultNFSCSIVersion           = "v4.9.0"
		defaultNFSDataDir              = "/data/nfs"
		defaultNFSExportSubnet         = "*"
		defaultChaosMeshVersion        = "2.7.1"
		defaultCPAOperatorVersion      = "v1.4.2"
		defaultCPAOperatorRelease      = "custom-pod-autoscaler-operator"
		defaultCPAOperatorNamespace    = "default"
		defaultMentatVersion           = "v0.3.0"
		defaultMentatSleep             = 5
		defaultMentatPingAttempts      = 5
		defaultMentatPingTimeout       = 1
		defaultMentatBandwidthPort     = 2113
		defaultMentatBandwidthBytes    = 256 * 1024
		defaultMentatBandwidthInterval = 90
		defaultMentatBandwidthJitter   = 30
		defaultMentatBandwidthTimeout  = 30
	)

	if n := c.NFS; n != nil {
		if n.VM.Name == "" {
			n.VM.Name = "nfs-server"
		}
		applyNodeDefaults(&n.VM)
		if n.VM.Cores == 0 {
			n.VM.Cores = 2
		}
		if n.VM.Memory == 0 {
			n.VM.Memory = 2048
		}
		if n.VM.DiskSize == 0 {
			n.VM.DiskSize = 100
		}
		if n.DataDir == "" {
			n.DataDir = defaultNFSDataDir
		}
		if n.ExportSubnet == "" {
			n.ExportSubnet = defaultNFSExportSubnet
		}
	}

	if r := c.Registry; r != nil {
		if r.VM.Name == "" {
			r.VM.Name = "harbor"
		}
		applyNodeDefaults(&r.VM)
		if r.VM.Cores == 0 {
			r.VM.Cores = 2
		}
		if r.VM.Memory == 0 {
			r.VM.Memory = 4096
		}
		if r.VM.DiskSize == 0 {
			r.VM.DiskSize = 80
		}
		if r.Harbor.Hostname == "" {
			r.Harbor.Hostname = r.VM.IP
		}
		if r.Harbor.AdminPassword == "" {
			r.Harbor.AdminPassword = "Harbor12345"
		}
		if r.Harbor.DataVolume == "" {
			r.Harbor.DataVolume = "/data"
		}
		if r.Harbor.HTTPPort == 0 {
			r.Harbor.HTTPPort = 80
		}
	}

	for i := range c.Clusters {
		spec := &c.Clusters[i]
		if spec.K3s.TaintControlPlane == nil {
			v := true
			spec.K3s.TaintControlPlane = &v
		}
		if spec.KubeconfigPath == "" {
			spec.KubeconfigPath = fmt.Sprintf("./kubeconfig-%s", spec.Name)
		}
		if spec.Addons.Cilium.Enabled && spec.Addons.Cilium.Version == "" {
			spec.Addons.Cilium.Version = defaultCiliumVersion
		}
		if spec.Addons.Cilium.Enabled && spec.Addons.Cilium.HubbleUINodePort == 0 {
			spec.Addons.Cilium.HubbleUINodePort = 32080
		}
		if spec.Addons.Monitoring.Enabled && spec.Addons.Monitoring.Version == "" {
			spec.Addons.Monitoring.Version = defaultMonitoringVersion
		}
		if spec.Addons.MonAgent.Enabled {
			if spec.Addons.MonAgent.Version == "" {
				spec.Addons.MonAgent.Version = defaultMonAgentVersion
			}
			if spec.Addons.MonAgent.PrometheusURL == "" {
				spec.Addons.MonAgent.PrometheusURL = "http://prometheus-stack-kube-prom-prometheus.observability:9090"
			}
			if spec.Addons.MonAgent.ScrapePeriodSeconds == 0 {
				spec.Addons.MonAgent.ScrapePeriodSeconds = 30
			}
			if spec.Addons.MonAgent.PromQLRange == "" {
				spec.Addons.MonAgent.PromQLRange = "5m"
			}
			if spec.Addons.MonAgent.NamespaceLabelSelector == "" {
				spec.Addons.MonAgent.NamespaceLabelSelector = "mon-agent/enabled=true"
			}
		}
		if spec.Addons.ClusterLens.Enabled {
			if spec.Addons.ClusterLens.Version == "" {
				spec.Addons.ClusterLens.Version = defaultClusterLensVersion
			}
			if spec.Addons.ClusterLens.NodePort == 0 {
				spec.Addons.ClusterLens.NodePort = 32088
			}
			if spec.Addons.ClusterLens.Refresh == "" {
				spec.Addons.ClusterLens.Refresh = "2s"
			}
		}
		if spec.Addons.Logging.Enabled {
			if spec.Addons.Logging.LokiVersion == "" {
				spec.Addons.Logging.LokiVersion = defaultLokiVersion
			}
			if spec.Addons.Logging.AlloyVersion == "" {
				spec.Addons.Logging.AlloyVersion = defaultAlloyVersion
			}
			if spec.Addons.Logging.LokiNodePort == 0 {
				spec.Addons.Logging.LokiNodePort = 32099
			}
			if spec.Addons.Logging.Retention == "" {
				spec.Addons.Logging.Retention = "30d"
			}
			if spec.Addons.Logging.StorageSize == "" {
				spec.Addons.Logging.StorageSize = "10Gi"
			}
		}
		if spec.Addons.Istio.Enabled && spec.Addons.Istio.Version == "" {
			spec.Addons.Istio.Version = defaultIstioVersion
		}
		if spec.Addons.Jaeger.Enabled {
			if spec.Addons.Jaeger.Version == "" {
				spec.Addons.Jaeger.Version = defaultJaegerVersion
			}
			if spec.Addons.Jaeger.NodePort == 0 {
				spec.Addons.Jaeger.NodePort = 30002
			}
			if spec.Addons.Jaeger.StorageClass == "" {
				spec.Addons.Jaeger.StorageClass = "local-path"
			}
			if spec.Addons.Jaeger.StorageSize == "" {
				spec.Addons.Jaeger.StorageSize = "10Gi"
			}
		}
		if spec.Addons.Istio.Enabled {
			if spec.Addons.Kiali.Version == "" {
				spec.Addons.Kiali.Version = defaultKialiVersion
			}
			if spec.Addons.Kiali.NodePort == 0 {
				spec.Addons.Kiali.NodePort = 30001
			}
			if spec.Addons.Kiali.SigningKey == "" {
				spec.Addons.Kiali.SigningKey = "proxmox-k3s-kiali"
			}
		}
		if spec.Addons.NFS.Enabled && spec.Addons.NFS.Version == "" {
			spec.Addons.NFS.Version = defaultNFSCSIVersion
		}
		if spec.Addons.NFS.Enabled && spec.Addons.NFS.DataDir == "" {
			spec.Addons.NFS.DataDir = defaultNFSDataDir
		}
		if spec.Addons.NFS.Enabled && spec.Addons.NFS.ExportSubnet == "" {
			spec.Addons.NFS.ExportSubnet = defaultNFSExportSubnet
		}
		if spec.Addons.ChaosMesh.Enabled {
			if spec.Addons.ChaosMesh.Version == "" {
				spec.Addons.ChaosMesh.Version = defaultChaosMeshVersion
			}
			if spec.Addons.ChaosMesh.DashboardNodePort == 0 {
				spec.Addons.ChaosMesh.DashboardNodePort = 32300
			}
		}
		if spec.Addons.CPAOperator.IsEnabled() {
			if spec.Addons.CPAOperator.Version == "" {
				spec.Addons.CPAOperator.Version = defaultCPAOperatorVersion
			}
			if spec.Addons.CPAOperator.Release == "" {
				spec.Addons.CPAOperator.Release = defaultCPAOperatorRelease
			}
			if spec.Addons.CPAOperator.Namespace == "" {
				spec.Addons.CPAOperator.Namespace = defaultCPAOperatorNamespace
			}
		}
		if spec.Addons.Mentat.Enabled {
			if spec.Addons.Mentat.Version == "" {
				spec.Addons.Mentat.Version = defaultMentatVersion
			}
			if spec.Addons.Mentat.SleepSeconds == 0 {
				spec.Addons.Mentat.SleepSeconds = defaultMentatSleep
			}
			if spec.Addons.Mentat.PingAttempts == 0 {
				spec.Addons.Mentat.PingAttempts = defaultMentatPingAttempts
			}
			if spec.Addons.Mentat.PingTimeoutSeconds == 0 {
				spec.Addons.Mentat.PingTimeoutSeconds = defaultMentatPingTimeout
			}
			if spec.Addons.Mentat.BandwidthPort == 0 {
				spec.Addons.Mentat.BandwidthPort = defaultMentatBandwidthPort
			}
			if spec.Addons.Mentat.BandwidthBytes == 0 {
				spec.Addons.Mentat.BandwidthBytes = defaultMentatBandwidthBytes
			}
			if spec.Addons.Mentat.BandwidthIntervalSeconds == 0 {
				spec.Addons.Mentat.BandwidthIntervalSeconds = defaultMentatBandwidthInterval
			}
			if spec.Addons.Mentat.BandwidthJitterSeconds == 0 {
				spec.Addons.Mentat.BandwidthJitterSeconds = defaultMentatBandwidthJitter
			}
			if spec.Addons.Mentat.BandwidthTimeoutSeconds == 0 {
				spec.Addons.Mentat.BandwidthTimeoutSeconds = defaultMentatBandwidthTimeout
			}
		}
		if spec.Addons.Registry != nil && spec.Addons.Registry.HTTPPort == 0 {
			spec.Addons.Registry.HTTPPort = 80
		}
		if spec.Addons.Monitoring.Enabled {
			if spec.Addons.Monitoring.PrometheusNodePort == 0 {
				spec.Addons.Monitoring.PrometheusNodePort = 32090
			}
			if spec.Addons.Monitoring.GrafanaNodePort == 0 {
				spec.Addons.Monitoring.GrafanaNodePort = 32000
			}
			if spec.Addons.Monitoring.GrafanaAdminPassword == "" {
				spec.Addons.Monitoring.GrafanaAdminPassword = "admin"
			}
		}
		for j := range spec.ControlPlane {
			applyNodeDefaults(&spec.ControlPlane[j])
		}
		for j := range spec.Workers {
			applyNodeDefaults(&spec.Workers[j].CPNode)
		}
	}
}

func applyNodeDefaults(node *CPNode) {
	if node.Bridge == "" {
		node.Bridge = "vmbr0"
	}
	if node.Cores == 0 {
		node.Cores = 2
	}
	if node.Memory == 0 {
		node.Memory = 2048
	}
	if node.DiskSize == 0 {
		node.DiskSize = 20
	}
	if node.Gateway != "" {
		if node.SubnetMask == 0 {
			node.SubnetMask = 24
		}
		if node.DNS == "" {
			node.DNS = "1.1.1.1"
		}
	}
}

func (c *Config) validate() error {
	if c.Proxmox.APIURL == "" {
		return fmt.Errorf("proxmox.api_url is required")
	}
	if c.Proxmox.TokenID == "" {
		return fmt.Errorf("proxmox.token_id is required")
	}
	if c.Proxmox.TokenSecret == "" {
		return fmt.Errorf("proxmox.token_secret is required")
	}
	return c.validateClusters()
}

func (c *Config) validateClusters() error {
	if len(c.Clusters) == 0 {
		// Infra-only config (template/registry/nfs commands) — cluster validation not applicable.
		return nil
	}
	if c.Template.CloudImageURL == "" {
		return fmt.Errorf("unknown os %q; set template.cloud_image_url explicitly", c.Template.OS)
	}
	if (c.Template.IP == "") != (c.Template.Gateway == "") {
		return fmt.Errorf("template.ip and template.gateway must be set together")
	}
	if c.Template.TimeoutSeconds <= 0 {
		return fmt.Errorf("template.timeout_seconds must be greater than 0")
	}
	if c.Template.CloneParallelism <= 0 {
		return fmt.Errorf("template.clone_parallelism must be greater than 0")
	}

	clusterNames := make(map[string]bool)
	for i, spec := range c.Clusters {
		if spec.Name == "" {
			return fmt.Errorf("clusters[%d].name is required", i)
		}
		if clusterNames[spec.Name] {
			return fmt.Errorf("duplicate cluster name %q", spec.Name)
		}
		clusterNames[spec.Name] = true

		if len(spec.ControlPlane) != 1 && len(spec.ControlPlane) != 3 {
			return fmt.Errorf("clusters[%d] (%s): control_plane must have 1 or 3 nodes, got %d",
				i, spec.Name, len(spec.ControlPlane))
		}
		for j, node := range spec.ControlPlane {
			if node.ProxmoxNode == "" {
				return fmt.Errorf("clusters[%d] (%s): control_plane[%d].proxmox_node is required",
					i, spec.Name, j)
			}
		}
	}

	if err := c.validateClusterMesh(clusterNames); err != nil {
		return err
	}

	if r := c.Registry; r != nil {
		if r.VM.ProxmoxNode == "" {
			return fmt.Errorf("registry.vm.proxmox_node is required")
		}
		if r.Harbor.Hostname == "" {
			return fmt.Errorf("registry.harbor.hostname is required when registry.vm.ip is not set")
		}
	}

	if n := c.NFS; n != nil {
		if n.VM.ProxmoxNode == "" {
			return fmt.Errorf("nfs.vm.proxmox_node is required")
		}
		if n.VM.IP == "" {
			return fmt.Errorf("nfs.vm.ip is required")
		}
		if n.DataDir != "" && !strings.HasPrefix(n.DataDir, "/") {
			return fmt.Errorf("nfs.data_dir must be an absolute path")
		}
	}

	for i, spec := range c.Clusters {
		seenPVCs := make(map[string]bool)
		for j, pvc := range spec.PVCs {
			if pvc.Name == "" || pvc.Namespace == "" || pvc.StorageClass == "" || pvc.Size == "" {
				return fmt.Errorf("clusters[%d] (%s): pvcs[%d] name, namespace, storageClass, and size are required", i, spec.Name, j)
			}
			key := pvc.Namespace + "/" + pvc.Name
			if seenPVCs[key] {
				return fmt.Errorf("clusters[%d] (%s): duplicate PVC %q", i, spec.Name, key)
			}
			seenPVCs[key] = true
		}
		if spec.Addons.NFS.Enabled && spec.Addons.NFS.Server == "" {
			return fmt.Errorf("clusters[%d] (%s): addons.nfs.enabled requires addons.nfs.server to be set", i, spec.Name)
		}
		if spec.Addons.Registry != nil && spec.Addons.Registry.Hostname == "" {
			return fmt.Errorf("clusters[%d] (%s): addons.registry.hostname is required", i, spec.Name)
		}
		if p := spec.Addons.Cilium.HubbleUINodePort; p != 0 && (p < 30000 || p > 32767) {
			return fmt.Errorf("clusters[%d] (%s): addons.cilium.hubble_ui_node_port must be 0 or in range 30000–32767", i, spec.Name)
		}
		if spec.Addons.Jaeger.Enabled && !spec.Addons.Istio.Enabled {
			return fmt.Errorf("clusters[%d] (%s): addons.jaeger.enabled requires addons.istio.enabled", i, spec.Name)
		}
		if p := spec.Addons.Jaeger.NodePort; p != 0 && (p < 30000 || p > 32767) {
			return fmt.Errorf("clusters[%d] (%s): addons.jaeger.node_port must be 0 or in range 30000–32767", i, spec.Name)
		}
		if p := spec.Addons.Kiali.NodePort; p != 0 && (p < 30000 || p > 32767) {
			return fmt.Errorf("clusters[%d] (%s): addons.kiali.node_port must be 0 or in range 30000–32767", i, spec.Name)
		}
		if p := spec.Addons.ChaosMesh.DashboardNodePort; p != 0 && (p < 30000 || p > 32767) {
			return fmt.Errorf("clusters[%d] (%s): addons.chaos_mesh.dashboard_node_port must be 0 or in range 30000–32767", i, spec.Name)
		}
		if spec.Addons.CPAOperator.IsEnabled() {
			if spec.Addons.CPAOperator.Version == "" || spec.Addons.CPAOperator.Release == "" || spec.Addons.CPAOperator.Namespace == "" {
				return fmt.Errorf("clusters[%d] (%s): addons.custom_pod_autoscaler version, release, and namespace are required when enabled", i, spec.Name)
			}
		}
		if spec.Addons.Mentat.Enabled {
			mentat := spec.Addons.Mentat
			if mentat.SleepSeconds <= 0 || mentat.PingAttempts <= 0 || mentat.PingTimeoutSeconds <= 0 ||
				mentat.BandwidthBytes <= 0 || mentat.BandwidthIntervalSeconds <= 0 || mentat.BandwidthTimeoutSeconds <= 0 {
				return fmt.Errorf("clusters[%d] (%s): mentat probe intervals, counts, sizes, and timeouts must be positive", i, spec.Name)
			}
			if mentat.BandwidthJitterSeconds < 0 {
				return fmt.Errorf("clusters[%d] (%s): addons.mentat.bandwidth_jitter_seconds must be non-negative", i, spec.Name)
			}
			if mentat.BandwidthPort < 1 || mentat.BandwidthPort > 65535 {
				return fmt.Errorf("clusters[%d] (%s): addons.mentat.bandwidth_port must be in range 1–65535", i, spec.Name)
			}
		}
		if p := spec.Addons.Logging.LokiNodePort; p != 0 && (p < 30000 || p > 32767) {
			return fmt.Errorf("clusters[%d] (%s): addons.logging.loki_node_port must be 0 or in range 30000–32767", i, spec.Name)
		}
		if spec.Addons.MonAgent.Enabled && spec.Addons.MonAgent.ScrapePeriodSeconds <= 0 {
			return fmt.Errorf("clusters[%d] (%s): addons.mon_agent.scrape_period_seconds must be positive", i, spec.Name)
		}
		if p := spec.Addons.ClusterLens.NodePort; p != 0 && (p < 30000 || p > 32767) {
			return fmt.Errorf("clusters[%d] (%s): addons.cluster_lens.node_port must be 0 or in range 30000–32767", i, spec.Name)
		}
		if spec.Addons.ClusterLens.Enabled {
			if _, err := time.ParseDuration(spec.Addons.ClusterLens.Refresh); err != nil {
				return fmt.Errorf("clusters[%d] (%s): addons.cluster_lens.refresh must be a duration such as 2s or 500ms", i, spec.Name)
			}
		}
		if spec.Addons.NFS.Enabled && spec.Addons.NFS.DataDir != "" && !strings.HasPrefix(spec.Addons.NFS.DataDir, "/") {
			return fmt.Errorf("clusters[%d] (%s): addons.nfs.data_dir must be an absolute path", i, spec.Name)
		}
	}

	return nil
}

func (c *Config) validateClusterMesh(clusterNames map[string]bool) error {
	if len(c.ClusterMesh) == 0 {
		return nil
	}

	usedIDs := make(map[int]bool)
	meshClusters := make([]ClusterSpec, 0, len(c.ClusterMesh))

	for i, entry := range c.ClusterMesh {
		if !clusterNames[entry.Cluster] {
			return fmt.Errorf("cluster_mesh[%d]: cluster %q is not defined in clusters", i, entry.Cluster)
		}
		if usedIDs[entry.ID] {
			return fmt.Errorf("cluster_mesh: duplicate id %d", entry.ID)
		}
		usedIDs[entry.ID] = true
		if entry.ID < 1 || entry.ID > 255 {
			return fmt.Errorf("cluster_mesh[%d]: id %d is out of range (must be 1–255)", i, entry.ID)
		}

		for _, spec := range c.Clusters {
			if spec.Name == entry.Cluster {
				if !spec.Addons.Cilium.Enabled {
					return fmt.Errorf("cluster_mesh: cluster %q is in the mesh but has Cilium disabled", spec.Name)
				}
				meshClusters = append(meshClusters, spec)
				break
			}
		}
	}

	// Verify no overlapping pod or service CIDRs among mesh clusters.
	for i := 0; i < len(meshClusters); i++ {
		for j := i + 1; j < len(meshClusters); j++ {
			a, b := meshClusters[i], meshClusters[j]
			if a.PodCIDR != "" && b.PodCIDR != "" && cidrsOverlap(a.PodCIDR, b.PodCIDR) {
				return fmt.Errorf("cluster_mesh: pod_cidr overlap between %s (%s) and %s (%s)",
					a.Name, a.PodCIDR, b.Name, b.PodCIDR)
			}
			if a.ServiceCIDR != "" && b.ServiceCIDR != "" && cidrsOverlap(a.ServiceCIDR, b.ServiceCIDR) {
				return fmt.Errorf("cluster_mesh: service_cidr overlap between %s (%s) and %s (%s)",
					a.Name, a.ServiceCIDR, b.Name, b.ServiceCIDR)
			}
		}
	}

	return nil
}

func cidrsOverlap(a, b string) bool {
	_, netA, err := net.ParseCIDR(a)
	if err != nil {
		return false
	}
	_, netB, err := net.ParseCIDR(b)
	if err != nil {
		return false
	}
	return netA.Contains(netB.IP) || netB.Contains(netA.IP)
}

func StateDirForCluster(clusterName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".proxmox-k3s", clusterName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

// firstCPNode returns the first control-plane node for use in template defaults.
func firstCPNode(c *Config) CPNode {
	if len(c.ControlPlane) > 0 {
		return c.ControlPlane[0]
	}
	if len(c.Clusters) > 0 && len(c.Clusters[0].ControlPlane) > 0 {
		return c.Clusters[0].ControlPlane[0]
	}
	return CPNode{}
}

func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func PrefixedNodeName(clusterName, nodeName string) string {
	if clusterName == "" || nodeName == "" {
		return nodeName
	}
	prefix := clusterName + "-"
	if strings.HasPrefix(nodeName, prefix) {
		return nodeName
	}
	return prefix + nodeName
}
