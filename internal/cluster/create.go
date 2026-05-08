package cluster

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/unict-cclab/proxmox-k3s/internal/addons"
	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/k3s"
	pxclient "github.com/unict-cclab/proxmox-k3s/internal/proxmox"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

// clusterState holds per-cluster data needed for post-creation steps
// (addon installation, cluster mesh wiring).
type clusterState struct {
	spec       config.ClusterSpec
	cpIP       string
	kubeconfig []byte
	keyPath    string
}

// Create provisions all clusters defined in the config.
func Create(ctx context.Context, cfg *config.Config, out io.Writer) error {
	return createMulti(ctx, cfg, out)
}

func createMulti(ctx context.Context, cfg *config.Config, out io.Writer) error {
	states := make([]*clusterState, 0, len(cfg.Clusters))

	for _, spec := range cfg.Clusters {
		clusterCfg := cfg.ToClusterConfig(spec)

		fmt.Fprintln(out)
		ui.Section(out, fmt.Sprintf("=== Cluster: %s ===", spec.ClusterName))

		if err := createSingle(ctx, clusterCfg, out); err != nil {
			return fmt.Errorf("cluster %s: %w", spec.ClusterName, err)
		}

		kubeconfig, err := os.ReadFile(clusterCfg.KubeconfigPath)
		if err != nil {
			return fmt.Errorf("reading kubeconfig for %s: %w", spec.ClusterName, err)
		}

		stateDir, err := config.StateDirForCluster(spec.ClusterName)
		if err != nil {
			return fmt.Errorf("state dir for %s: %w", spec.ClusterName, err)
		}
		keyPair, err := util.EnsureKeyPair(stateDir)
		if err != nil {
			return fmt.Errorf("key pair for %s: %w", spec.ClusterName, err)
		}

		st := &clusterState{
			spec:       spec,
			cpIP:       clusterCfg.ControlPlane[0].IP,
			kubeconfig: kubeconfig,
			keyPath:    keyPair.PrivateKeyPath,
		}
		states = append(states, st)

		if cfg.Addons.Cilium.Enabled || cfg.Addons.Monitoring.Enabled {
			if err := installAddons(cfg, st, out); err != nil {
				return err
			}
		}
	}

	if cfg.Addons.Cilium.Enabled && cfg.Addons.Cilium.ClusterMesh && len(states) > 1 {
		if err := connectMesh(cfg, states, out); err != nil {
			return err
		}
	}

	fmt.Fprintln(out)
	ui.Success(out, "All clusters ready — %d cluster(s)", len(states))
	for _, st := range states {
		ui.Info(out, "  %s  kubeconfig → %s", st.spec.ClusterName, st.spec.KubeconfigPath)
		if cfg.Addons.Monitoring.Enabled {
			ui.Info(out, "  %s  Prometheus :%d  Grafana :%d",
				st.spec.ClusterName,
				cfg.Addons.Monitoring.PrometheusNodePort,
				cfg.Addons.Monitoring.GrafanaNodePort,
			)
		}
	}
	return nil
}

func installAddons(cfg *config.Config, st *clusterState, out io.Writer) error {
	runner, err := util.DialWithKey(st.cpIP, 22, "ubuntu", st.keyPath)
	if err != nil {
		return fmt.Errorf("SSH to %s CP: %w", st.spec.ClusterName, err)
	}
	defer runner.Close()

	if err := addons.EnsureHelm(runner, out); err != nil {
		return fmt.Errorf("[%s] Helm: %w", st.spec.ClusterName, err)
	}

	if cfg.Addons.Cilium.Enabled {
		if err := addons.InstallCilium(runner, cfg.Addons.Cilium,
			st.spec.ClusterName, st.spec.CiliumClusterID, out); err != nil {
			return err
		}
	}

	if cfg.Addons.Monitoring.Enabled {
		if err := addons.InstallMonitoring(runner, cfg.Addons.Monitoring,
			st.spec.ClusterName, out); err != nil {
			return err
		}
	}

	return nil
}

// connectMesh enables cluster mesh on every cluster, then connects them pairwise.
func connectMesh(cfg *config.Config, states []*clusterState, out io.Writer) error {
	fmt.Fprintln(out)
	ui.Section(out, "=== Cilium cluster mesh ===")

	for _, st := range states {
		runner, err := util.DialWithKey(st.cpIP, 22, "ubuntu", st.keyPath)
		if err != nil {
			return fmt.Errorf("SSH to %s: %w", st.spec.ClusterName, err)
		}
		err = addons.EnableClusterMesh(runner, st.spec.ClusterName, out)
		runner.Close()
		if err != nil {
			return err
		}
	}

	// Connect each unique pair (i, j) where i < j; cilium connect is bidirectional.
	for i, source := range states {
		dests := make(map[string][]byte)
		for _, dest := range states[i+1:] {
			dests[dest.spec.ClusterName] = dest.kubeconfig
		}
		if len(dests) == 0 {
			continue
		}

		runner, err := util.DialWithKey(source.cpIP, 22, "ubuntu", source.keyPath)
		if err != nil {
			return fmt.Errorf("SSH to %s: %w", source.spec.ClusterName, err)
		}
		err = addons.ConnectClusterMesh(runner, source.spec.ClusterName,
			source.kubeconfig, dests, out)
		runner.Close()
		if err != nil {
			return err
		}
	}

	ui.Success(out, "All clusters connected via Cilium cluster mesh")
	return nil
}

func createSingle(ctx context.Context, cfg *config.Config, out io.Writer) error {
	stateDir, err := config.StateDirForCluster(cfg.ClusterName)
	if err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}

	ui.Section(out, "SSH key pair")
	keyPair, err := util.EnsureKeyPair(stateDir)
	if err != nil {
		return fmt.Errorf("SSH key pair: %w", err)
	}
	ui.Info(out, "private key: %s", keyPair.PrivateKeyPath)

	ui.Section(out, "Connecting to Proxmox")
	px, err := pxclient.New(cfg)
	if err != nil {
		return err
	}

	if cfg.K3s.Version == "" {
		ui.Section(out, "Detecting latest k3s version")
		ver, err := k3s.LatestK3sVersion(ctx)
		if err != nil {
			return fmt.Errorf("detecting k3s version: %w", err)
		}
		cfg.K3s.Version = ver
		ui.Success(out, "using k3s %s", ver)
	}

	ui.Section(out, "VM template")
	if err := pxclient.EnsureTemplate(ctx, px, cfg, keyPair.PrivateKeyPath, keyPair.PublicKey, out); err != nil {
		return err
	}

	// Allocate all VM IDs in one Proxmox scan: CP nodes first, workers after.
	vmidBase, err := px.NextVMID(ctx, cfg.Template.VMIDBase+1)
	if err != nil {
		return fmt.Errorf("allocating VMIDs: %w", err)
	}

	ui.Section(out, "Control-plane VMs")
	cpVMs, err := createControlPlaneVMs(ctx, px, cfg, keyPair.PublicKey, vmidBase, out)
	if err != nil {
		return err
	}

	ui.Section(out, "Worker VMs")
	workerVMs, err := createWorkerVMs(ctx, px, cfg, keyPair.PublicKey, vmidBase+len(cfg.ControlPlane), out)
	if err != nil {
		return err
	}

	installer := k3s.New(cfg, keyPair.PrivateKeyPath, out)

	ui.Section(out, "Installing k3s")
	token, firstCP, err := installControlPlane(installer, cfg, cpVMs)
	if err != nil {
		return err
	}
	defer firstCP.Runner.Close()

	serverURL := "https://" + firstCP.IP + ":6443"

	if err := installWorkers(ctx, installer, serverURL, token, workerVMs); err != nil {
		return err
	}

	ui.Section(out, "Applying node labels and taints")
	if err := applyLabelsAndTaints(installer, firstCP, cfg, workerVMs); err != nil {
		return err
	}

	ui.Section(out, "Fetching kubeconfig")
	kubeconfig, err := installer.FetchKubeconfig(firstCP, firstCP.IP, cfg.ClusterName)
	if err != nil {
		return err
	}

	if err := os.WriteFile(cfg.KubeconfigPath, []byte(kubeconfig), 0600); err != nil {
		return fmt.Errorf("writing kubeconfig to %s: %w", cfg.KubeconfigPath, err)
	}
	fmt.Fprintln(out)
	ui.Success(out, "Cluster ready! Kubeconfig saved to %s", cfg.KubeconfigPath)
	ui.Info(out, "export KUBECONFIG=%s", cfg.KubeconfigPath)
	ui.Info(out, "kubectl get nodes")
	return nil
}

func createControlPlaneVMs(ctx context.Context, px *pxclient.Client, cfg *config.Config, pubKey string, vmidBase int, out io.Writer) ([]*pxclient.VMInfo, error) {
	specs := make([]pxclient.VMSpec, 0, len(cfg.ControlPlane))
	for i, node := range cfg.ControlPlane {
		specs = append(specs, pxclient.VMSpec{
			VMID:        vmidBase + i,
			Name:        config.PrefixedNodeName(cfg.ClusterName, node.Name),
			ProxmoxNode: node.ProxmoxNode,
			Cores:       node.Cores,
			Memory:      node.Memory,
			DiskSize:    node.DiskSize,
			Storage:     node.Storage,
			Bridge:      node.Bridge,
			IPAddress:   node.IP,
			Gateway:     node.Gateway,
			DNS:         node.DNS,
			SubnetMask:  node.SubnetMask,
			User:        "ubuntu",
			SSHPubKey:   pubKey,
			ClusterName: cfg.ClusterName,
			Role:        "server",
		})
	}

	vms, err := createVMsParallel(ctx, px, cfg, specs, out)
	if err != nil {
		return nil, fmt.Errorf("creating control-plane VMs: %w", err)
	}
	return vms, nil
}

func createWorkerVMs(ctx context.Context, px *pxclient.Client, cfg *config.Config, pubKey string, vmidBase int, out io.Writer) ([]*pxclient.VMInfo, error) {
	specs := make([]pxclient.VMSpec, 0, len(cfg.Workers))
	for i, node := range cfg.Workers {
		specs = append(specs, pxclient.VMSpec{
			VMID:        vmidBase + i,
			Name:        config.PrefixedNodeName(cfg.ClusterName, node.Name),
			ProxmoxNode: node.ProxmoxNode,
			Cores:       node.Cores,
			Memory:      node.Memory,
			DiskSize:    node.DiskSize,
			Storage:     node.Storage,
			Bridge:      node.Bridge,
			IPAddress:   node.IP,
			Gateway:     node.Gateway,
			DNS:         node.DNS,
			SubnetMask:  node.SubnetMask,
			User:        "ubuntu",
			SSHPubKey:   pubKey,
			ClusterName: cfg.ClusterName,
			Role:        "agent",
			Labels:      node.Labels,
			Taints:      node.Taints,
		})
	}

	vms, err := createVMsParallel(ctx, px, cfg, specs, out)
	if err != nil {
		return nil, fmt.Errorf("creating worker VMs: %w", err)
	}
	return vms, nil
}

func createVMsParallel(ctx context.Context, px *pxclient.Client, cfg *config.Config, specs []pxclient.VMSpec, out io.Writer) ([]*pxclient.VMInfo, error) {
	results := make([]*pxclient.VMInfo, len(specs))
	var outMu sync.Mutex

	g, ctx := errgroup.WithContext(ctx)
	for i, spec := range specs {
		i, spec := i, spec
		g.Go(func() error {
			var vmLog bytes.Buffer
			vmInfo, err := pxclient.CreateVM(ctx, px, cfg, spec, &vmLog)
			writePrefixedLogs(&outMu, out, spec.Name, vmLog.String())
			if err != nil {
				return fmt.Errorf("%s: %w", spec.Name, err)
			}
			results[i] = vmInfo
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}

func writePrefixedLogs(mu *sync.Mutex, out io.Writer, name, logs string) {
	if strings.TrimSpace(logs) == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()

	for _, line := range strings.Split(strings.TrimRight(logs, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fmt.Fprintf(out, "  [%s] %s\n", name, line)
	}
}

func installControlPlane(installer *k3s.Installer, cfg *config.Config, cpVMs []*pxclient.VMInfo) (string, *k3s.NodeInfo, error) {
	ha := len(cfg.ControlPlane) == 3

	first, err := installer.ConnectNode(cpVMs[0].IP, cpVMs[0].Name)
	if err != nil {
		return "", nil, err
	}

	token, err := installer.InstallFirstServer(first, ha)
	if err != nil {
		first.Runner.Close()
		return "", nil, err
	}

	serverURL := "https://" + first.IP + ":6443"
	for _, vm := range cpVMs[1:] {
		node, err := installer.ConnectNode(vm.IP, vm.Name)
		if err != nil {
			first.Runner.Close()
			return "", nil, err
		}
		if err := installer.InstallAdditionalServer(node, serverURL, token); err != nil {
			node.Runner.Close()
			first.Runner.Close()
			return "", nil, err
		}
		node.Runner.Close()
	}

	return token, first, nil
}

func installWorkers(ctx context.Context, installer *k3s.Installer, serverURL, token string, workerVMs []*pxclient.VMInfo) error {
	g, ctx := errgroup.WithContext(ctx)
	for _, vm := range workerVMs {
		vm := vm
		g.Go(func() error {
			node, err := installer.ConnectNode(vm.IP, vm.Name)
			if err != nil {
				return err
			}
			defer node.Runner.Close()
			return installer.InstallAgent(node, serverURL, token)
		})
	}
	return g.Wait()
}

func applyLabelsAndTaints(installer *k3s.Installer, cpNode *k3s.NodeInfo, cfg *config.Config, workerVMs []*pxclient.VMInfo) error {
	for i, worker := range cfg.Workers {
		if len(worker.Labels) == 0 && len(worker.Taints) == 0 {
			continue
		}
		if err := installer.ApplyNodeLabels(cpNode, workerVMs[i].Name, worker.Labels, worker.Taints); err != nil {
			return err
		}
	}
	return nil
}
