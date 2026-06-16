package addons

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

const (
	ciliumRepo         = "https://helm.cilium.io/"
	ciliumRelease      = "cilium"
	ciliumNamespace    = "kube-system"
	ciliumMeshNodePort = 32379
)

// ciliumValuesTemplate is the Helm values template for Cilium.
// Format args: cpIP, clusterName, clusterID (int), hubbleUIServiceBlock.
const ciliumValuesTemplate = `kubeProxyReplacement: true
k8sServiceHost: %s
k8sServicePort: "6443"
cluster:
  name: %s
  id: %d
hubble:
  relay:
    enabled: true
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
    - key: "ManagementOnly"
      operator: "Equal"
      value: "true"
      effect: "NoSchedule"
  ui:
    enabled: true
%s    affinity:
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
    - key: "ManagementOnly"
      operator: "Equal"
      value: "true"
      effect: "NoSchedule"
operator:
  replicas: 1
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
  - key: "ManagementOnly"
    operator: "Equal"
    value: "true"
    effect: "NoSchedule"
ipam:
  mode: kubernetes
`

// ciliumHubbleUIServiceBlock is injected into ciliumValuesTemplate when HubbleUINodePort > 0.
// Format arg: nodePort (int).
const ciliumHubbleUIServiceBlock = "    service:\n      type: NodePort\n      nodePort: %d\n"

// InstallCilium installs Cilium via Helm and waits for it to be ready.
// When inMesh is true the clustermesh-apiserver is also deployed on a NodePort.
func InstallCilium(runner *util.Runner, cilium config.CiliumConfig, clusterName, cpIP string, clusterID int, inMesh bool, out io.Writer) error {
	ui.Step(out, "[%s] adding Cilium Helm repo...", clusterName)
	if err := helmAddRepo(runner, "cilium", ciliumRepo, out); err != nil {
		return err
	}

	hubbleUIServiceBlock := ""
	if cilium.HubbleUINodePort > 0 {
		hubbleUIServiceBlock = fmt.Sprintf(ciliumHubbleUIServiceBlock, cilium.HubbleUINodePort)
	}

	values := fmt.Sprintf(ciliumValuesTemplate, cpIP, clusterName, clusterID, hubbleUIServiceBlock)

	ui.Step(out, "[%s] installing Cilium %s (cluster-id=%d)...", clusterName, cilium.Version, clusterID)
	chart := fmt.Sprintf("cilium/cilium --version %s", cilium.Version)
	if err := helmInstall(runner, ciliumRelease, chart, ciliumNamespace, values, "", out); err != nil {
		return err
	}

	// Cilium DaemonSet pods use hostNetwork so they start before nodes are Ready,
	// but we still need to confirm they're running before proceeding.
	ui.Step(out, "[%s] waiting for Cilium agents...", clusterName)
	waitPods := "kubectl wait --for=condition=ready pod -l k8s-app=cilium -n kube-system --timeout=5m"
	if err := runner.Run(waitPods, out); err != nil {
		return fmt.Errorf("[%s] Cilium agents not ready: %w", clusterName, err)
	}

	ui.Step(out, "[%s] waiting for nodes to become Ready...", clusterName)
	waitNodes := "kubectl wait --for=condition=ready node --all --timeout=5m"
	if err := runner.Run(waitNodes, out); err != nil {
		return fmt.Errorf("[%s] nodes not ready after Cilium install: %w", clusterName, err)
	}

	ui.Success(out, "[%s] Cilium ready", clusterName)
	return nil
}

// EnableClusterMesh installs the cilium CLI and runs `cilium clustermesh enable`.
func EnableClusterMesh(runner *util.Runner, clusterName string, out io.Writer) error {
	ui.Step(out, "[%s] enabling cluster mesh...", clusterName)

	if err := installCiliumCLI(runner, out); err != nil {
		return err
	}

	// The enable command can time out while certificate-generation jobs are
	// still in progress on a fresh cluster. Retry a few times before giving up.
	const enableRetries = 5
	const enableRetryDelay = 30 * time.Second
	for attempt := 1; attempt <= enableRetries; attempt++ {
		err := runner.Run("cilium clustermesh enable --service-type NodePort", out)
		if err == nil {
			break
		}
		if attempt == enableRetries {
			return fmt.Errorf("[%s] cilium clustermesh enable: %w", clusterName, err)
		}
		ui.Warn(out, "[%s] cilium clustermesh enable failed (attempt %d/%d), retrying in %s...",
			clusterName, attempt, enableRetries, enableRetryDelay)
		time.Sleep(enableRetryDelay)
	}

	if err := runner.Run("cilium clustermesh status --wait --wait-duration 10m", out); err != nil {
		return fmt.Errorf("[%s] cluster mesh not ready: %w", clusterName, err)
	}

	ui.Success(out, "[%s] cluster mesh enabled", clusterName)
	return nil
}

// ConnectClusterMesh connects sourceCluster to each cluster in destClusters.
// sourceKubeconfig is the rewritten kubeconfig for the source cluster (with the
// cluster's external IP and context name matching ClusterName).
// destKubeconfigs maps cluster-name → kubeconfig content.
//
// The cilium CLI is run on the source CP node via SSH. Both the source and
// destination kubeconfigs are uploaded there so the CLI can reach both API servers.
func ConnectClusterMesh(runner *util.Runner, sourceCluster string, sourceKubeconfig []byte, destKubeconfigs map[string][]byte, out io.Writer) error {
	if len(destKubeconfigs) == 0 {
		return nil
	}

	// Upload source kubeconfig (has correct IP + context name, unlike /etc/rancher/k3s/k3s.yaml).
	srcPath := fmt.Sprintf("/tmp/kube-%s.yaml", sourceCluster)
	if err := runner.WriteFile(srcPath, sourceKubeconfig); err != nil {
		return fmt.Errorf("uploading kubeconfig for %s: %w", sourceCluster, err)
	}

	for destName, destKube := range destKubeconfigs {
		destPath := fmt.Sprintf("/tmp/kube-%s.yaml", destName)
		if err := runner.WriteFile(destPath, destKube); err != nil {
			return fmt.Errorf("uploading kubeconfig for %s: %w", destName, err)
		}

		ui.Step(out, "[%s] connecting to %s...", sourceCluster, destName)
		// KUBECONFIG with both configs lets the cilium CLI resolve both contexts.
		cmd := fmt.Sprintf(
			"KUBECONFIG=%s:%s cilium clustermesh connect --context %s --destination-context %s --allow-mismatching-ca",
			srcPath, destPath, sourceCluster, destName,
		)
		if err := runner.Run(cmd, out); err != nil {
			return fmt.Errorf("connecting %s → %s: %w", sourceCluster, destName, err)
		}
	}

	ui.Success(out, "[%s] connected to %s", sourceCluster, strings.Join(keys(destKubeconfigs), ", "))
	return nil
}

func installCiliumCLI(runner *util.Runner, out io.Writer) error {
	if _, err := runner.Output("cilium version 2>/dev/null"); err == nil {
		return nil // already installed
	}

	ui.Step(out, "installing Cilium CLI...")
	script := strings.Join([]string{
		`CILIUM_CLI_VERSION=$(curl -fsSL https://raw.githubusercontent.com/cilium/cilium-cli/main/stable.txt)`,
		`curl -fsSL --remote-name-all https://github.com/cilium/cilium-cli/releases/download/${CILIUM_CLI_VERSION}/cilium-linux-amd64.tar.gz`,
		`sudo tar xzvfC cilium-linux-amd64.tar.gz /usr/local/bin`,
		`rm -f cilium-linux-amd64.tar.gz`,
	}, " && ")

	if err := runner.Run(script, out); err != nil {
		return fmt.Errorf("installing Cilium CLI: %w", err)
	}
	return nil
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
