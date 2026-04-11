package api

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/amarchese96/proxmox-k3s/internal/cluster"
	"github.com/amarchese96/proxmox-k3s/internal/config"
	"github.com/amarchese96/proxmox-k3s/internal/k3s"
	pxclient "github.com/amarchese96/proxmox-k3s/internal/proxmox"
	"github.com/amarchese96/proxmox-k3s/internal/util"
)

type Service struct{}

func New() *Service {
	return &Service{}
}

func (s *Service) CreateCluster(ctx context.Context, cfg *config.Config, out io.Writer) error {
	return cluster.Create(ctx, cfg, out)
}

func (s *Service) DeleteCluster(ctx context.Context, cfg *config.Config, deleteTemplate bool, out io.Writer) error {
	return cluster.Delete(ctx, cfg, deleteTemplate, out)
}

func (s *Service) CreateTemplate(ctx context.Context, cfg *config.Config, out io.Writer) error {
	px, err := pxclient.New(cfg)
	if err != nil {
		return err
	}
	return pxclient.EnsureTemplate(ctx, px, cfg, out)
}

func (s *Service) DeleteTemplate(ctx context.Context, cfg *config.Config, out io.Writer) error {
	px, err := pxclient.New(cfg)
	if err != nil {
		return err
	}
	return pxclient.DeleteTemplate(ctx, px, cfg, out)
}

func (s *Service) RefreshKubeconfig(ctx context.Context, cfg *config.Config, out io.Writer) error {
	stateDir, err := config.StateDirForCluster(cfg.ClusterName)
	if err != nil {
		return err
	}

	keyPair, err := util.EnsureKeyPair(stateDir)
	if err != nil {
		return err
	}

	px, err := pxclient.New(cfg)
	if err != nil {
		return err
	}

	cpName := fmt.Sprintf("%s-cp-01", cfg.ClusterName)
	vm, err := px.FindVMByName(ctx, cpName)
	if err != nil || vm == nil {
		return fmt.Errorf("control-plane VM %q not found; has the cluster been created?", cpName)
	}

	ip := cfg.ControlPlane.IPStart
	if ip == "" {
		ip, err = pxclient.WaitForIP(ctx, vm, 30e9)
		if err != nil {
			return fmt.Errorf("getting CP IP: %w", err)
		}
	}

	runner, err := util.DialWithKey(ip, 22, "ubuntu", keyPair.PrivateKeyPath)
	if err != nil {
		return fmt.Errorf("SSH to %s: %w", ip, err)
	}
	defer runner.Close()

	raw, err := runner.Output("sudo cat /etc/rancher/k3s/k3s.yaml")
	if err != nil {
		return fmt.Errorf("reading kubeconfig: %w", err)
	}

	kubeconfig := rewriteKubeconfig(raw, ip, cfg.ClusterName)
	if err := os.WriteFile(cfg.KubeconfigPath, []byte(kubeconfig), 0600); err != nil {
		return fmt.Errorf("writing kubeconfig: %w", err)
	}

	fmt.Fprintf(out, "Kubeconfig saved to %s\n", cfg.KubeconfigPath)
	return nil
}

func (s *Service) DetectLatestK3sVersion(ctx context.Context) (string, error) {
	return k3s.LatestK3sVersion(ctx)
}

func rewriteKubeconfig(raw, ip, clusterName string) string {
	r := raw
	r = replaceAll(r, "https://127.0.0.1:6443", "https://"+ip+":6443")
	r = replaceAll(r, "name: default", "name: "+clusterName)
	r = replaceAll(r, "cluster: default", "cluster: "+clusterName)
	r = replaceAll(r, "user: default", "user: "+clusterName)
	r = replaceAll(r, "current-context: default", "current-context: "+clusterName)
	return r
}

func replaceAll(s, old, new string) string {
	result := ""
	for {
		idx := indexOf(s, old)
		if idx < 0 {
			result += s
			break
		}
		result += s[:idx] + new
		s = s[idx+len(old):]
	}
	return result
}

func indexOf(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
