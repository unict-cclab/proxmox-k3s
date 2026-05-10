package addons

import (
	"fmt"
	"io"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

const (
	nfsCSIRepo      = "https://raw.githubusercontent.com/kubernetes-csi/csi-driver-nfs/master/charts"
	nfsCSINamespace = "kube-system"
)

// InstallNFSCSI installs the NFS CSI driver via Helm and creates a StorageClass
// pointing to the cluster's dedicated subdirectory on the NFS server.
func InstallNFSCSI(runner *util.Runner, nfsServerIP, clusterName, dataDir string, addon config.NFSAddonConfig, out io.Writer) error {
	ui.Step(out, "[%s] adding NFS CSI Helm repo...", clusterName)
	if err := helmAddRepo(runner, "csi-driver-nfs", nfsCSIRepo, out); err != nil {
		return err
	}

	// Controller prefers the management node pool; node DaemonSet tolerates the
	// management taint so it can also run there when NFS PVCs are used by
	// management workloads.
	values := `controller:
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
node:
  tolerations:
  - key: "nodepool"
    operator: "Equal"
    value: "management"
    effect: "NoSchedule"
`

	chart := fmt.Sprintf("csi-driver-nfs/csi-driver-nfs --version %s", addon.Version)
	ui.Step(out, "[%s] installing NFS CSI driver %s...", clusterName, addon.Version)
	if err := helmInstall(runner, "csi-driver-nfs", chart, nfsCSINamespace, values, "", out); err != nil {
		return fmt.Errorf("[%s] NFS CSI driver: %w", clusterName, err)
	}

	// Create a StorageClass backed by the cluster's dedicated NFS share.
	share := fmt.Sprintf("%s/%s", dataDir, clusterName)
	storageClass := fmt.Sprintf(`apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: nfs
provisioner: nfs.csi.k8s.io
parameters:
  server: %s
  share: %s
  mountPermissions: "0"
reclaimPolicy: Retain
volumeBindingMode: Immediate
allowVolumeExpansion: true
`, nfsServerIP, share)

	ui.Step(out, "[%s] creating NFS StorageClass (server=%s share=%s)...", clusterName, nfsServerIP, share)
	if err := runner.WriteFile("/tmp/nfs-storageclass.yaml", []byte(storageClass)); err != nil {
		return fmt.Errorf("[%s] writing NFS StorageClass: %w", clusterName, err)
	}
	if err := runner.Run("kubectl apply -f /tmp/nfs-storageclass.yaml", out); err != nil {
		return fmt.Errorf("[%s] applying NFS StorageClass: %w", clusterName, err)
	}

	ui.Success(out, "[%s] NFS CSI ready — StorageClass: nfs → %s:%s", clusterName, nfsServerIP, share)
	return nil
}
