package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

	// Shared across all modes
	Proxmox  ProxmoxConfig  `yaml:"proxmox"`
	Template TemplateConfig `yaml:"template"`

	// Single-cluster k3s and node definitions
	K3s          K3sConfig    `yaml:"k3s"`
	ControlPlane []CPNode     `yaml:"control_plane"`
	Workers      []WorkerNode `yaml:"workers"`

	// Multi-cluster fields; presence of Clusters activates multi-cluster mode
	Clusters []ClusterSpec `yaml:"clusters"`
	Addons   AddonsConfig  `yaml:"addons"`
}

// IsMultiCluster reports whether the config defines multiple clusters.
func (c *Config) IsMultiCluster() bool { return len(c.Clusters) > 0 }

// ClusterSpec is the per-cluster definition used in multi-cluster configs.
type ClusterSpec struct {
	ClusterName     string       `yaml:"cluster_name"`
	KubeconfigPath  string       `yaml:"kubeconfig_path"`
	CiliumClusterID int          `yaml:"cilium_cluster_id"`
	K3s             K3sConfig    `yaml:"k3s"`
	ControlPlane    []CPNode     `yaml:"control_plane"`
	Workers         []WorkerNode `yaml:"workers"`
}

// AddonsConfig lists optional software installed on every cluster after k3s is up.
type AddonsConfig struct {
	Cilium     CiliumAddon     `yaml:"cilium"`
	Monitoring MonitoringAddon `yaml:"monitoring"`
}

// CiliumAddon configures Cilium CNI installation and optional cluster mesh.
type CiliumAddon struct {
	Enabled     bool   `yaml:"enabled"`
	Version     string `yaml:"version"`
	ClusterMesh bool   `yaml:"cluster_mesh"`
}

// MonitoringAddon configures the kube-prometheus-stack installation.
type MonitoringAddon struct {
	Enabled              bool   `yaml:"enabled"`
	PrometheusNodePort   int    `yaml:"prometheus_node_port"`
	GrafanaNodePort      int    `yaml:"grafana_node_port"`
	GrafanaAdminPassword string `yaml:"grafana_admin_password"`
}

type ProxmoxConfig struct {
	APIURL      string `yaml:"api_url"`
	TokenID     string `yaml:"token_id"`
	TokenSecret string `yaml:"token_secret"`
	InsecureTLS bool   `yaml:"insecure_tls"`
}

type TemplateConfig struct {
	Name           string `yaml:"name"`
	ProxmoxNode    string `yaml:"proxmox_node"`
	Storage        string `yaml:"storage"`
	ImageStorage   string `yaml:"image_storage"`
	Bridge         string `yaml:"bridge"`
	IP             string `yaml:"ip"`
	Gateway        string `yaml:"gateway"`
	DNS            string `yaml:"dns"`
	SubnetMask     int    `yaml:"subnet_mask"`
	DiskSize       int    `yaml:"disk_size"`
	Password       string `yaml:"password"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
	OS             string `yaml:"os"`
	CloudImageURL  string `yaml:"cloud_image_url"`
	VMIDBase       int    `yaml:"vmid_base"`
}

type K3sConfig struct {
	Version         string `yaml:"version"`
	ExtraServerArgs string `yaml:"extra_server_args"`
	ExtraAgentArgs  string `yaml:"extra_agent_args"`
}

type CPNode struct {
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
	CPNode  `yaml:",inline"`
	Labels  []string `yaml:"labels"`
	Taints  []string `yaml:"taints"`
}

var KnownOSImages = map[string]string{
	"ubuntu-24.04": "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img",
	"ubuntu-22.04": "https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img",
	"debian-12":    "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2",
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

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
		ClusterName:    spec.ClusterName,
		KubeconfigPath: spec.KubeconfigPath,
		Proxmox:        c.Proxmox,
		Template:       c.Template,
		K3s:            spec.K3s,
		ControlPlane:   spec.ControlPlane,
		Workers:        spec.Workers,
	}
	cfg.applyDefaults()
	return cfg
}

func (c *Config) applyDefaults() {
	// Determine the first CP node for template fallbacks.
	firstCP := firstCPNode(c)

	// Template defaults (shared in both modes)
	if c.Template.Name == "" {
		// In multi-cluster mode ClusterName is empty; use a generic name.
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
	if c.Template.CloudImageURL == "" {
		if u, ok := KnownOSImages[c.Template.OS]; ok {
			c.Template.CloudImageURL = u
		}
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
	if c.Addons.Cilium.Enabled && c.Addons.Cilium.Version == "" {
		c.Addons.Cilium.Version = "1.16.5"
	}
	if c.Addons.Monitoring.Enabled {
		if c.Addons.Monitoring.PrometheusNodePort == 0 {
			c.Addons.Monitoring.PrometheusNodePort = 32090
		}
		if c.Addons.Monitoring.GrafanaNodePort == 0 {
			c.Addons.Monitoring.GrafanaNodePort = 32000
		}
		if c.Addons.Monitoring.GrafanaAdminPassword == "" {
			c.Addons.Monitoring.GrafanaAdminPassword = "admin"
		}
	}

	for i := range c.Clusters {
		spec := &c.Clusters[i]
		if spec.KubeconfigPath == "" {
			spec.KubeconfigPath = fmt.Sprintf("./kubeconfig-%s", spec.ClusterName)
		}
		if spec.CiliumClusterID == 0 {
			spec.CiliumClusterID = i + 1
		}
		// When Cilium is the CNI, k3s must start without flannel.
		if c.Addons.Cilium.Enabled {
			const ciliumArgs = "--flannel-backend=none --disable-network-policy"
			if !strings.Contains(spec.K3s.ExtraServerArgs, "--flannel-backend=none") {
				if spec.K3s.ExtraServerArgs != "" {
					spec.K3s.ExtraServerArgs += " " + ciliumArgs
				} else {
					spec.K3s.ExtraServerArgs = ciliumArgs
				}
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
	if node.Storage == "" {
		node.Storage = "local-lvm"
	}
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
		return fmt.Errorf("clusters list is required — use a clusters: list with at least one entry")
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

	usedNames := make(map[string]bool)
	usedIDs := make(map[int]bool)
	for i, spec := range c.Clusters {
		if spec.ClusterName == "" {
			return fmt.Errorf("clusters[%d].cluster_name is required", i)
		}
		if usedNames[spec.ClusterName] {
			return fmt.Errorf("duplicate cluster_name %q", spec.ClusterName)
		}
		usedNames[spec.ClusterName] = true

		if len(spec.ControlPlane) != 1 && len(spec.ControlPlane) != 3 {
			return fmt.Errorf("clusters[%d] (%s): control_plane must have 1 or 3 nodes, got %d",
				i, spec.ClusterName, len(spec.ControlPlane))
		}
		for j, node := range spec.ControlPlane {
			if node.ProxmoxNode == "" {
				return fmt.Errorf("clusters[%d] (%s): control_plane[%d].proxmox_node is required",
					i, spec.ClusterName, j)
			}
		}

		if c.Addons.Cilium.Enabled && c.Addons.Cilium.ClusterMesh {
			if usedIDs[spec.CiliumClusterID] {
				return fmt.Errorf("duplicate cilium_cluster_id %d", spec.CiliumClusterID)
			}
			usedIDs[spec.CiliumClusterID] = true
		}
	}
	return nil
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
