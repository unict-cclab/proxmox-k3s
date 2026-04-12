package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ClusterName    string `yaml:"cluster_name"`
	KubeconfigPath string `yaml:"kubeconfig_path"`

	Proxmox      ProxmoxConfig  `yaml:"proxmox"`
	Template     TemplateConfig `yaml:"template"`
	K3s          K3sConfig      `yaml:"k3s"`
	ControlPlane []CPNode       `yaml:"control_plane"`
	Workers      []WorkerNode   `yaml:"workers"`
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
	Name        string   `yaml:"name"`
	ProxmoxNode string   `yaml:"proxmox_node"`
	Storage     string   `yaml:"storage"`
	Bridge      string   `yaml:"bridge"`
	Cores       int      `yaml:"cores"`
	Memory      int      `yaml:"memory"`
	DiskSize    int      `yaml:"disk_size"`
	IP          string   `yaml:"ip"`
	Gateway     string   `yaml:"gateway"`
	DNS         string   `yaml:"dns"`
	SubnetMask  int      `yaml:"subnet_mask"`
	Labels      []string `yaml:"labels"`
	Taints      []string `yaml:"taints"`
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

	// Template falls back to first CP node where possible
	firstCP := CPNode{}
	if len(c.ControlPlane) > 0 {
		firstCP = c.ControlPlane[0]
	}
	if c.Template.Name == "" {
		c.Template.Name = c.ClusterName + "-tmpl"
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

	for i := range c.ControlPlane {
		applyNodeDefaults(&c.ControlPlane[i].Storage, &c.ControlPlane[i].Bridge,
			&c.ControlPlane[i].Cores, &c.ControlPlane[i].Memory,
			&c.ControlPlane[i].DiskSize, &c.ControlPlane[i].SubnetMask,
			&c.ControlPlane[i].DNS, c.ControlPlane[i].Gateway)
	}
	for i := range c.Workers {
		applyNodeDefaults(&c.Workers[i].Storage, &c.Workers[i].Bridge,
			&c.Workers[i].Cores, &c.Workers[i].Memory,
			&c.Workers[i].DiskSize, &c.Workers[i].SubnetMask,
			&c.Workers[i].DNS, c.Workers[i].Gateway)
	}
}

func applyNodeDefaults(storage, bridge *string, cores, memory, diskSize, subnetMask *int, dns *string, gateway string) {
	if *storage == "" {
		*storage = "local-lvm"
	}
	if *bridge == "" {
		*bridge = "vmbr0"
	}
	if *cores == 0 {
		*cores = 2
	}
	if *memory == 0 {
		*memory = 2048
	}
	if *diskSize == 0 {
		*diskSize = 20
	}
	if gateway != "" {
		if *subnetMask == 0 {
			*subnetMask = 24
		}
		if *dns == "" {
			*dns = "1.1.1.1"
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
	if len(c.ControlPlane) != 1 && len(c.ControlPlane) != 3 {
		return fmt.Errorf("control_plane must have 1 (standalone) or 3 (HA) nodes, got %d", len(c.ControlPlane))
	}
	for i, node := range c.ControlPlane {
		if node.ProxmoxNode == "" {
			return fmt.Errorf("control_plane[%d].proxmox_node is required", i)
		}
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
