package cluster

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/amarchese96/proxmox-k3s/internal/config"
	"github.com/amarchese96/proxmox-k3s/internal/k3s"
	pxclient "github.com/amarchese96/proxmox-k3s/internal/proxmox"
	"github.com/amarchese96/proxmox-k3s/internal/ui"
	"github.com/amarchese96/proxmox-k3s/internal/util"
)

func Create(ctx context.Context, cfg *config.Config, out io.Writer) error {
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
	if err := pxclient.EnsureTemplate(ctx, px, cfg, out); err != nil {
		return err
	}

	ui.Section(out, "Control-plane VMs")
	cpVMs, err := createControlPlaneVMs(ctx, px, cfg, keyPair.PublicKey, out)
	if err != nil {
		return err
	}

	ui.Section(out, "Worker VMs")
	workerVMs, err := createWorkerVMs(ctx, px, cfg, keyPair.PublicKey, out)
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

	if err := installWorkers(installer, serverURL, token, workerVMs); err != nil {
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

func createControlPlaneVMs(ctx context.Context, px *pxclient.Client, cfg *config.Config, pubKey string, out io.Writer) ([]*pxclient.VMInfo, error) {
	cp := cfg.ControlPlane
	specs := make([]pxclient.VMSpec, 0, cp.Count)

	for idx := 0; idx < cp.Count; idx++ {
		name := fmt.Sprintf("%s-cp-%02d", cfg.ClusterName, idx+1)
		spec := pxclient.VMSpec{
			Name:        name,
			ProxmoxNode: resolveNode(cp.ProxmoxNodes, cp.ProxmoxNode, idx),
			Cores:       cp.Cores,
			Memory:      cp.Memory,
			DiskSize:    cp.DiskSize,
			Storage:     cp.Storage,
			Bridge:      cfg.NodeDefaults.Bridge,
			User:        "ubuntu",
			SSHPubKey:   pubKey,
			ClusterName: cfg.ClusterName,
			Role:        "server",
		}
		if cfg.Networking.Gateway != "" && cp.IPStart != "" {
			spec.IPAddress = incrementIP(cp.IPStart, idx)
			spec.Gateway = cfg.Networking.Gateway
			spec.DNS = cfg.Networking.DNS
			spec.SubnetMask = cfg.Networking.SubnetMask
		}
		specs = append(specs, spec)
	}

	nextVMIDStart, err := px.NextVMID(ctx, cfg.Template.VMIDBase+1)
	if err != nil {
		return nil, fmt.Errorf("allocating starting VMID for control-plane: %w", err)
	}
	assignVMIDs(nextVMIDStart, specs)
	vms, err := createVMsParallel(ctx, px, cfg, specs, out)
	if err != nil {
		return nil, fmt.Errorf("creating control-plane VMs: %w", err)
	}
	return vms, nil
}

func createWorkerVMs(ctx context.Context, px *pxclient.Client, cfg *config.Config, pubKey string, out io.Writer) (map[string][]*pxclient.VMInfo, error) {
	result := make(map[string][]*pxclient.VMInfo)
	nextVMIDStart, err := px.NextVMID(ctx, cfg.Template.VMIDBase+1)
	if err != nil {
		return nil, fmt.Errorf("allocating starting VMID for workers: %w", err)
	}

	for _, pool := range cfg.WorkerPools {
		ui.Step(out, "pool %q (%d nodes)", pool.Name, pool.Count)
		specs := make([]pxclient.VMSpec, 0, pool.Count)

		for idx := 0; idx < pool.Count; idx++ {
			name := fmt.Sprintf("%s-%s-%02d", cfg.ClusterName, pool.Name, idx+1)
			spec := pxclient.VMSpec{
				Name:        name,
				ProxmoxNode: resolveNode(pool.ProxmoxNodes, pool.ProxmoxNode, idx),
				Cores:       pool.Cores,
				Memory:      pool.Memory,
				DiskSize:    pool.DiskSize,
				Storage:     pool.Storage,
				Bridge:      cfg.NodeDefaults.Bridge,
				User:        "ubuntu",
				SSHPubKey:   pubKey,
				ClusterName: cfg.ClusterName,
				Role:        "agent",
				Labels:      pool.Labels,
				Taints:      pool.Taints,
			}
			if cfg.Networking.Gateway != "" && pool.IPStart != "" {
				spec.IPAddress = incrementIP(pool.IPStart, idx)
				spec.Gateway = cfg.Networking.Gateway
				spec.DNS = cfg.Networking.DNS
				spec.SubnetMask = cfg.Networking.SubnetMask
			}
			specs = append(specs, spec)
		}

		nextVMIDStart = assignVMIDs(nextVMIDStart, specs)
		poolVMs, err := createVMsParallel(ctx, px, cfg, specs, out)
		if err != nil {
			return nil, fmt.Errorf("creating worker pool %s: %w", pool.Name, err)
		}
		result[pool.Name] = poolVMs
	}
	return result, nil
}

func assignVMIDs(start int, specs []pxclient.VMSpec) int {
	next := start
	for i := range specs {
		if specs[i].VMID != 0 {
			next = specs[i].VMID + 1
			continue
		}
		specs[i].VMID = next
		next++
	}
	return next
}

func createVMsParallel(ctx context.Context, px *pxclient.Client, cfg *config.Config, specs []pxclient.VMSpec, out io.Writer) ([]*pxclient.VMInfo, error) {
	results := make([]*pxclient.VMInfo, len(specs))
	errs := make([]error, len(specs))

	var (
		wg    sync.WaitGroup
		outMu sync.Mutex
	)
	for i := range specs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var vmLog bytes.Buffer
			vmInfo, err := pxclient.CreateVM(ctx, px, cfg, specs[idx], &vmLog)
			flushPrefixedLogs(&outMu, out, specs[idx].Name, vmLog.String())
			if err != nil {
				errs[idx] = fmt.Errorf("%s: %w", specs[idx].Name, err)
				return
			}
			results[idx] = vmInfo
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}

func flushPrefixedLogs(mu *sync.Mutex, out io.Writer, name, logs string) {
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
	ha := cfg.ControlPlane.Count == 3

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

func installWorkers(installer *k3s.Installer, serverURL, token string, pools map[string][]*pxclient.VMInfo) error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for poolName, vms := range pools {
		for _, vm := range vms {
			wg.Add(1)
			go func(name, ip, pool string) {
				defer wg.Done()
				node, err := installer.ConnectNode(ip, name)
				if err != nil {
					mu.Lock()
					errs = append(errs, fmt.Errorf("[%s] %w", pool, err))
					mu.Unlock()
					return
				}
				defer node.Runner.Close()
				if err := installer.InstallAgent(node, serverURL, token); err != nil {
					mu.Lock()
					errs = append(errs, fmt.Errorf("[%s] %w", pool, err))
					mu.Unlock()
				}
			}(vm.Name, vm.IP, poolName)
		}
	}
	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("worker installation errors: %v", errs)
	}
	return nil
}

func applyLabelsAndTaints(installer *k3s.Installer, cpNode *k3s.NodeInfo, cfg *config.Config, pools map[string][]*pxclient.VMInfo) error {
	for _, pool := range cfg.WorkerPools {
		if len(pool.Labels) == 0 && len(pool.Taints) == 0 {
			continue
		}
		for j := range pools[pool.Name] {
			nodeName := fmt.Sprintf("%s-%s-%02d", cfg.ClusterName, pool.Name, j+1)
			if err := installer.ApplyNodeLabels(cpNode, nodeName, pool.Labels, pool.Taints); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveNode(perVM []string, fallback string, idx int) string {
	if idx < len(perVM) && perVM[idx] != "" {
		return perVM[idx]
	}
	return fallback
}

func incrementIP(base string, n int) string {
	parts := splitDots(base)
	if len(parts) != 4 {
		return base
	}
	return fmt.Sprintf("%s.%s.%s.%d", parts[0], parts[1], parts[2], atoi(parts[3])+n)
}

func splitDots(s string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '.' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return parts
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}
