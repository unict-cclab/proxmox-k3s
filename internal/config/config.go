package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ClusterName    string `yaml:"cluster_name"`
	KubeconfigPath string `yaml:"kubeconfig_path"`

	Proxmox      ProxmoxConfig      `yaml:"proxmox"`
	Template     TemplateConfig     `yaml:"template"`
	NodeDefaults NodeDefaults       `yaml:"node_defaults"`
	Networking   Networking         `yaml:"networking"`
	K3s          K3sConfig          `yaml:"k3s"`
	ControlPlane ControlPlaneConfig `yaml:"control_plane"`
	WorkerPools  []WorkerPool       `yaml:"worker_pools"`
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
	CloudInitStorage string `yaml:"cloud_init_storage"`
	OS               string `yaml:"os"`
	CloudImageURL    string `yaml:"cloud_image_url"`
	VMIDBase         int    `yaml:"vmid_base"`
}

type NodeDefaults struct {
	ProxmoxNode string `yaml:"proxmox_node"`
	Storage     string `yaml:"storage"`
	Cores       int    `yaml:"cores"`
	Memory      int    `yaml:"memory"`
	DiskSize    int    `yaml:"disk_size"`
	Bridge      string `yaml:"bridge"`
}

type Networking struct {
	Gateway    string `yaml:"gateway"`
	DNS        string `yaml:"dns"`
	SubnetMask int    `yaml:"subnet_mask"` // CIDR prefix length, default 24
}

type K3sConfig struct {
	Version         string `yaml:"version"`
	ExtraServerArgs string `yaml:"extra_server_args"`
	ExtraAgentArgs  string `yaml:"extra_agent_args"`
}

type ControlPlaneConfig struct {
	Count        int      `yaml:"count"`
	ProxmoxNode  string   `yaml:"proxmox_node"`
	ProxmoxNodes []string `yaml:"proxmox_nodes"`
	Storage      string   `yaml:"storage"`
	Cores        int      `yaml:"cores"`
	Memory       int      `yaml:"memory"`
	DiskSize     int      `yaml:"disk_size"`
	IPStart      string   `yaml:"ip_start"`
}

type WorkerPool struct {
	Name         string   `yaml:"name"`
	Count        int      `yaml:"count"`
	ProxmoxNode  string   `yaml:"proxmox_node"`
	ProxmoxNodes []string `yaml:"proxmox_nodes"`
	Storage      string   `yaml:"storage"`
	Cores        int      `yaml:"cores"`
	Memory       int      `yaml:"memory"`
	DiskSize     int      `yaml:"disk_size"`
	IPStart      string   `yaml:"ip_start"`
	Labels       []string `yaml:"labels"`
	Taints       []string `yaml:"taints"`
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

func (c *Config) applyDefaults() {
	if c.ClusterName == "" {
		c.ClusterName = "k3s-cluster"
	}
	if c.KubeconfigPath == "" {
		c.KubeconfigPath = "./kubeconfig"
	}

	if c.Template.ProxmoxNode == "" {
		c.Template.ProxmoxNode = c.NodeDefaults.ProxmoxNode
	}
	if c.Template.Name == "" {
		c.Template.Name = c.ClusterName + "-tmpl"
	}
	if c.Template.Storage == "" {
		c.Template.Storage = c.NodeDefaults.Storage
	}
	if c.Template.ImageStorage == "" {
		c.Template.ImageStorage = c.Template.Storage
	}
	if c.Template.CloudInitStorage == "" {
		c.Template.CloudInitStorage = c.Template.Storage
	}
	if c.Template.OS == "" {
		c.Template.OS = "ubuntu-24.04"
	}
	if c.Template.VMIDBase == 0 {
		c.Template.VMIDBase = 8000
	}
	if c.Template.CloudImageURL == "" {
		if u, ok := KnownOSImages[c.Template.OS]; ok {
			c.Template.CloudImageURL = u
		}
	}

	if c.NodeDefaults.Cores == 0 {
		c.NodeDefaults.Cores = 2
	}
	if c.NodeDefaults.Memory == 0 {
		c.NodeDefaults.Memory = 2048
	}
	if c.NodeDefaults.DiskSize == 0 {
		c.NodeDefaults.DiskSize = 20
	}
	if c.NodeDefaults.Bridge == "" {
		c.NodeDefaults.Bridge = "vmbr0"
	}
	if c.NodeDefaults.Storage == "" {
		c.NodeDefaults.Storage = "local-lvm"
	}

	if c.Networking.SubnetMask == 0 {
		c.Networking.SubnetMask = 24
	}
	if c.Networking.DNS == "" && c.Networking.Gateway != "" {
		c.Networking.DNS = "1.1.1.1"
	}

	if c.ControlPlane.Count == 0 {
		c.ControlPlane.Count = 1
	}
	c.ControlPlane.ProxmoxNode = coalesce(c.ControlPlane.ProxmoxNode, c.NodeDefaults.ProxmoxNode)
	c.ControlPlane.Storage = coalesce(c.ControlPlane.Storage, c.NodeDefaults.Storage)
	if c.ControlPlane.Cores == 0 {
		c.ControlPlane.Cores = c.NodeDefaults.Cores
	}
	if c.ControlPlane.Memory == 0 {
		c.ControlPlane.Memory = c.NodeDefaults.Memory
	}
	if c.ControlPlane.DiskSize == 0 {
		c.ControlPlane.DiskSize = c.NodeDefaults.DiskSize
	}

	for i := range c.WorkerPools {
		p := &c.WorkerPools[i]
		p.ProxmoxNode = coalesce(p.ProxmoxNode, c.NodeDefaults.ProxmoxNode)
		p.Storage = coalesce(p.Storage, c.NodeDefaults.Storage)
		if p.Cores == 0 {
			p.Cores = c.NodeDefaults.Cores
		}
		if p.Memory == 0 {
			p.Memory = c.NodeDefaults.Memory
		}
		if p.DiskSize == 0 {
			p.DiskSize = c.NodeDefaults.DiskSize
		}
		if p.Count == 0 {
			p.Count = 1
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
	if c.NodeDefaults.ProxmoxNode == "" && c.ControlPlane.ProxmoxNode == "" {
		return fmt.Errorf("node_defaults.proxmox_node or control_plane.proxmox_node is required")
	}
	if c.ControlPlane.Count != 1 && c.ControlPlane.Count != 3 {
		return fmt.Errorf("control_plane.count must be 1 (standalone) or 3 (HA)")
	}
	if c.Template.CloudImageURL == "" {
		return fmt.Errorf("unknown os %q; set template.cloud_image_url explicitly", c.Template.OS)
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

func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
