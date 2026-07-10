package addons

import (
	"strings"
	"testing"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
)

func TestRenderPVCsManifest(t *testing.T) {
	manifest, err := renderPVCsManifest([]config.PVCConfig{
		{Name: "data", Namespace: "default", StorageClass: "local-path", Size: "10Gi"},
		{Name: "shared", Namespace: "apps", StorageClass: "nfs-csi", Size: "50Gi"},
	})
	if err != nil {
		t.Fatal(err)
	}

	rendered := string(manifest)
	for _, expected := range []string{
		"kind: PersistentVolumeClaim",
		"name: data",
		"namespace: apps",
		"storageClassName: nfs-csi",
		"storage: 50Gi",
		"accessModes:\n        - ReadWriteOnce",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("rendered manifest does not contain %q:\n%s", expected, rendered)
		}
	}
	if strings.Count(rendered, "kind: PersistentVolumeClaim") != 2 {
		t.Fatalf("expected two PVC documents:\n%s", rendered)
	}
}
