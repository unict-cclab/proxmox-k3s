package addons

import (
	"fmt"
	"io"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
	"gopkg.in/yaml.v3"
)

type pvcManifest struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		AccessModes      []string `yaml:"accessModes"`
		StorageClassName string   `yaml:"storageClassName"`
		Resources        struct {
			Requests map[string]string `yaml:"requests"`
		} `yaml:"resources"`
	} `yaml:"spec"`
}

func renderPVCsManifest(pvcs []config.PVCConfig) ([]byte, error) {
	var result []byte
	for i, pvc := range pvcs {
		manifest := pvcManifest{APIVersion: "v1", Kind: "PersistentVolumeClaim"}
		manifest.Metadata.Name = pvc.Name
		manifest.Metadata.Namespace = pvc.Namespace
		manifest.Spec.AccessModes = []string{"ReadWriteOnce"}
		manifest.Spec.StorageClassName = pvc.StorageClass
		manifest.Spec.Resources.Requests = map[string]string{"storage": pvc.Size}

		data, err := yaml.Marshal(manifest)
		if err != nil {
			return nil, fmt.Errorf("marshal PVC %s/%s: %w", pvc.Namespace, pvc.Name, err)
		}
		if i > 0 {
			result = append(result, []byte("---\n")...)
		}
		result = append(result, data...)
	}
	return result, nil
}

// InstallPVCs creates the configured persistent volume claims in a cluster.
func InstallPVCs(runner *util.Runner, pvcs []config.PVCConfig, clusterName string, out io.Writer) error {
	if len(pvcs) == 0 {
		return nil
	}
	ui.Step(out, "[%s] creating persistent volume claims...", clusterName)
	manifest, err := renderPVCsManifest(pvcs)
	if err != nil {
		return err
	}
	if err := runner.WriteFile("/tmp/proxmox-k3s-pvcs.yaml", manifest); err != nil {
		return fmt.Errorf("[%s] writing PVC manifest: %w", clusterName, err)
	}
	if err := runner.Run("kubectl apply -f /tmp/proxmox-k3s-pvcs.yaml", out); err != nil {
		return fmt.Errorf("[%s] applying PVCs: %w", clusterName, err)
	}
	ui.Success(out, "[%s] %d persistent volume claim(s) configured", clusterName, len(pvcs))
	return nil
}
