package cluster

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/unict-cclab/proxmox-k3s/internal/addons"
	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/k3s"
	pxclient "github.com/unict-cclab/proxmox-k3s/internal/proxmox"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

// clusterState holds per-cluster data needed for post-creation steps.
type clusterState struct {
	spec       config.ClusterSpec
	cpIP       string
	kubeconfig []byte
	keyPath    string
}

// Setup provisions everything end-to-end:
// template → registry → clusters → mesh.
// Use this when starting from scratch.
func Setup(ctx context.Context, cfg *config.Config, out io.Writer) error {
	keyPath, err := cfg.SSHKeyFilePath()
	if err != nil {
		return fmt.Errorf("SSH key path: %w", err)
	}

	px, err := pxclient.New(cfg)
	if err != nil {
		return err
	}

	if cfg.HasTemplateConfig() {
		keyPair, err := util.EnsureKeyPairAt(keyPath)
		if err != nil {
			return fmt.Errorf("SSH key pair: %w", err)
		}
		ui.Section(out, "VM template")
		if err := pxclient.EnsureTemplate(ctx, px, cfg, keyPair.PrivateKeyPath, keyPair.PublicKey, out); err != nil {
			return err
		}
	} else {
		ui.Step(out, "verifying templates exist...")
		if err := checkTemplatesExist(ctx, px, cfg); err != nil {
			return err
		}
	}

	if err := resolveK3sVersions(ctx, cfg, out); err != nil {
		return err
	}

	if cfg.Registry != nil {
		if _, err := CreateRegistry(ctx, cfg, out); err != nil {
			return err
		}
	}

	if cfg.NFS != nil {
		if err := CreateNFSServer(ctx, cfg, out); err != nil {
			return err
		}
	}

	states, err := provisionClusters(ctx, cfg, px, keyPath, out)
	if err != nil {
		return err
	}
	if len(cfg.ClusterMesh) > 1 && allMeshClustersPresent(cfg, states) {
		return connectMesh(cfg, states, out)
	}
	return nil
}

// Create provisions clusters only. The VM template must already exist and,
// if a registry is configured, it must be reachable. Returns an error with
// a clear remediation hint if either prerequisite is not met.
func Create(ctx context.Context, cfg *config.Config, out io.Writer) error {
	keyPath, err := cfg.SSHKeyFilePath()
	if err != nil {
		return fmt.Errorf("SSH key path: %w", err)
	}

	px, err := pxclient.New(cfg)
	if err != nil {
		return err
	}

	ui.Step(out, "verifying prerequisites...")
	if err := checkTemplatesExist(ctx, px, cfg); err != nil {
		return err
	}

	for _, spec := range cfg.Clusters {
		if spec.Addons.Registry != nil {
			if err := checkRegistryReachable(config.HarborConfig{
				Hostname: spec.Addons.Registry.Hostname,
				HTTPPort: spec.Addons.Registry.HTTPPort,
			}); err != nil {
				return fmt.Errorf("cluster %s: %w", spec.Name, err)
			}
		}
	}
	ui.Success(out, "prerequisites OK")

	if err := resolveK3sVersions(ctx, cfg, out); err != nil {
		return err
	}

	_, err = provisionClusters(ctx, cfg, px, keyPath, out)
	return err
}

// provisionClusters pre-allocates VMIDs, creates all clusters in parallel,
// installs addons, and connects the mesh when all mesh clusters are present.
// The caller is responsible for ensuring templates exist before calling.
func provisionClusters(ctx context.Context, cfg *config.Config, px *pxclient.Client, keyPath string, out io.Writer) ([]*clusterState, error) {
	// Pre-allocate VMID ranges sequentially to avoid races during parallel creation.
	nextVMID := cfg.Template.VMIDBase + 1
	vmidBases := make([]int, len(cfg.Clusters))
	for i, spec := range cfg.Clusters {
		base, err := px.NextVMID(ctx, nextVMID)
		if err != nil {
			return nil, fmt.Errorf("allocating VMIDs for cluster %s: %w", spec.Name, err)
		}
		vmidBases[i] = base
		nextVMID = base + len(spec.ControlPlane) + len(spec.Workers)
	}

	states := make([]*clusterState, len(cfg.Clusters))

	g, gctx := errgroup.WithContext(ctx)
	for i, spec := range cfg.Clusters {
		i, spec := i, spec
		g.Go(func() error {
			pw := util.NewPrefixWriter(spec.Name, out)
			clusterCfg := cfg.ToClusterConfig(spec)

			var registryEndpoint string
			if spec.Addons.Registry != nil {
				registryEndpoint = fmt.Sprintf("http://%s:%d", spec.Addons.Registry.Hostname, spec.Addons.Registry.HTTPPort)
			}

			if err := createSingle(gctx, clusterCfg, vmidBases[i], registryEndpoint, pw); err != nil {
				return fmt.Errorf("cluster %s: %w", spec.Name, err)
			}

			kp, err := util.EnsureKeyPairAt(keyPath)
			if err != nil {
				return fmt.Errorf("key pair for %s: %w", spec.Name, err)
			}

			kubeconfig, err := os.ReadFile(clusterCfg.KubeconfigPath)
			if err != nil {
				return fmt.Errorf("reading kubeconfig for %s: %w", spec.Name, err)
			}

			if err := installAddons(cfg, &clusterState{
				spec:    spec,
				cpIP:    clusterCfg.ControlPlane[0].IP,
				keyPath: kp.PrivateKeyPath,
			}, pw); err != nil {
				return err
			}

			states[i] = &clusterState{
				spec:       spec,
				cpIP:       clusterCfg.ControlPlane[0].IP,
				kubeconfig: kubeconfig,
				keyPath:    kp.PrivateKeyPath,
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return states, err
	}

	fmt.Fprintln(out)
	ui.Success(out, "All clusters ready — %d cluster(s)", len(states))
	for _, st := range states {
		ui.Info(out, "  %s  kubeconfig → %s", st.spec.Name, st.spec.KubeconfigPath)
		if st.spec.Addons.Monitoring.Enabled {
			ui.Info(out, "  %s  Prometheus :%d  Grafana :%d",
				st.spec.Name,
				st.spec.Addons.Monitoring.PrometheusNodePort,
				st.spec.Addons.Monitoring.GrafanaNodePort,
			)
		}
		if st.spec.Addons.Istio.Enabled {
			ui.Info(out, "  %s  Kiali :%d", st.spec.Name, st.spec.Addons.Kiali.NodePort)
			if st.spec.Addons.Jaeger.Enabled {
				ui.Info(out, "  %s  Jaeger :%d", st.spec.Name, st.spec.Addons.Jaeger.NodePort)
			}
		}
		if st.spec.Addons.ChaosMesh.Enabled {
			ui.Info(out, "  %s  Chaos Mesh dashboard :%d", st.spec.Name, st.spec.Addons.ChaosMesh.DashboardNodePort)
		}
	}
	return states, nil
}

// checkTemplateExists returns an error with a remediation hint if the
// configured VM template is not found in Proxmox.
func checkTemplateExists(ctx context.Context, px *pxclient.Client, cfg *config.Config) error {
	name := pxclient.TemplateName(cfg)
	vm, err := px.FindVMByName(ctx, name)
	if err != nil {
		return fmt.Errorf("looking up template: %w", err)
	}
	if vm == nil {
		return fmt.Errorf("template %q not found — run 'proxmox-k3s template create' first", name)
	}
	return nil
}

// collectTemplateNames returns the deduplicated set of template VM names referenced
// by all node specs. Falls back to cfg.Template.Name for nodes without an override.
func collectTemplateNames(cfg *config.Config) []string {
	seen := map[string]bool{}
	for _, spec := range cfg.Clusters {
		for _, node := range spec.ControlPlane {
			name := node.Template
			if name == "" {
				name = cfg.Template.Name
			}
			seen[name] = true
		}
		for _, node := range spec.Workers {
			name := node.Template
			if name == "" {
				name = cfg.Template.Name
			}
			seen[name] = true
		}
	}
	if len(seen) == 0 {
		seen[cfg.Template.Name] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	return names
}

// checkTemplatesExist verifies that every unique template name referenced by
// node specs exists in Proxmox, falling back to cfg.Template.Name when a node
// does not specify its own template.
func checkTemplatesExist(ctx context.Context, px *pxclient.Client, cfg *config.Config) error {
	for _, name := range collectTemplateNames(cfg) {
		vm, err := px.FindVMByName(ctx, name)
		if err != nil {
			return fmt.Errorf("looking up template %q: %w", name, err)
		}
		if vm == nil {
			return fmt.Errorf("template %q not found — run 'proxmox-k3s template create' first", name)
		}
	}
	return nil
}

// checkRegistryReachable returns an error with a remediation hint if the
// Harbor ping endpoint does not respond with HTTP 200.
func checkRegistryReachable(harbor config.HarborConfig) error {
	url := fmt.Sprintf("http://%s:%d/api/v2.0/ping", harbor.Hostname, harbor.HTTPPort)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("registry at %s is not reachable — run 'proxmox-k3s registry create' first", url)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry at %s returned HTTP %d — is it running?", url, resp.StatusCode)
	}
	return nil
}

// resolveK3sVersions detects the latest k3s release once and fills it in for
// any cluster that did not specify a version.
func resolveK3sVersions(ctx context.Context, cfg *config.Config, out io.Writer) error {
	needsDetection := false
	for _, spec := range cfg.Clusters {
		if spec.K3s.Version == "" {
			needsDetection = true
			break
		}
	}
	if !needsDetection {
		return nil
	}

	ui.Section(out, "Detecting latest k3s version")
	ver, err := k3s.LatestK3sVersion(ctx)
	if err != nil {
		return fmt.Errorf("detecting k3s version: %w", err)
	}
	ui.Success(out, "using k3s %s", ver)

	for i := range cfg.Clusters {
		if cfg.Clusters[i].K3s.Version == "" {
			cfg.Clusters[i].K3s.Version = ver
		}
	}
	return nil
}

// SetupMesh connects the clusters listed in cfg.ClusterMesh into a Cilium cluster mesh.
// It reads kubeconfigs from disk and uses the static IPs from the config, so all mesh
// clusters must already be running and their kubeconfig files must exist.
func SetupMesh(ctx context.Context, cfg *config.Config, out io.Writer) error {
	if len(cfg.ClusterMesh) < 2 {
		return fmt.Errorf("cluster_mesh requires at least 2 entries")
	}

	keyPath, err := cfg.SSHKeyFilePath()
	if err != nil {
		return fmt.Errorf("SSH key path: %w", err)
	}

	ui.Step(out, "verifying cluster connectivity...")
	for _, entry := range cfg.ClusterMesh {
		spec, err := findClusterSpec(cfg, entry.Cluster)
		if err != nil {
			return err
		}
		runner, err := util.DialWithKey(spec.ControlPlane[0].IP, 22, config.VMSSHUser, keyPath)
		if err != nil {
			return fmt.Errorf("cluster %q control plane not reachable at %s — run 'proxmox-k3s cluster create' first",
				entry.Cluster, spec.ControlPlane[0].IP)
		}
		runner.Close()
	}
	ui.Success(out, "all clusters reachable")

	states := make([]*clusterState, 0, len(cfg.ClusterMesh))
	for _, entry := range cfg.ClusterMesh {
		spec, _ := findClusterSpec(cfg, entry.Cluster)

		kubeconfig, err := os.ReadFile(spec.KubeconfigPath)
		if err != nil {
			return fmt.Errorf("reading kubeconfig for %s: %w", spec.Name, err)
		}

		states = append(states, &clusterState{
			spec:       spec,
			cpIP:       spec.ControlPlane[0].IP,
			kubeconfig: kubeconfig,
			keyPath:    keyPath,
		})
	}

	return connectMesh(cfg, states, out)
}

func findClusterSpec(cfg *config.Config, name string) (config.ClusterSpec, error) {
	for _, s := range cfg.Clusters {
		if s.Name == name {
			return s, nil
		}
	}
	return config.ClusterSpec{}, fmt.Errorf("cluster %q listed in cluster_mesh not found in clusters", name)
}

func allMeshClustersPresent(cfg *config.Config, states []*clusterState) bool {
	for _, entry := range cfg.ClusterMesh {
		found := false
		for _, st := range states {
			if st.spec.Name == entry.Cluster {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// setupRegistry provisions the Harbor VM and installs Harbor on it.
func setupRegistry(ctx context.Context, cfg *config.Config, px *pxclient.Client, vmid int, keyPair *util.KeyPair, out io.Writer) (string, error) {
	r := cfg.Registry
	ui.Section(out, "=== Registry VM ===")

	spec := pxclient.VMSpec{
		VMID:        vmid,
		Name:        r.VM.Name,
		ProxmoxNode: r.VM.ProxmoxNode,
		Cores:       r.VM.Cores,
		Memory:      r.VM.Memory,
		DiskSize:    r.VM.DiskSize,
		Storage:     r.VM.Storage,
		Bridge:      r.VM.Bridge,
		IPAddress:   r.VM.IP,
		Gateway:     r.VM.Gateway,
		DNS:         r.VM.DNS,
		SubnetMask:  r.VM.SubnetMask,
		User:        config.VMSSHUser,
		SSHPubKey:   keyPair.PublicKey,
		ClusterName: "harbor",
		Role:        "registry",
	}

	vmInfo, err := pxclient.CreateVM(ctx, px, cfg, spec, out)
	if err != nil {
		return "", fmt.Errorf("registry VM: %w", err)
	}

	runner, err := util.WaitForSSH(vmInfo.IP, 22, config.VMSSHUser, keyPair.PrivateKeyPath, 5*time.Minute)
	if err != nil {
		return "", fmt.Errorf("SSH to registry VM: %w", err)
	}
	defer runner.Close()

	if err := addons.InstallHarbor(runner, r.Harbor, out); err != nil {
		return "", err
	}

	endpoint := fmt.Sprintf("http://%s:%d", r.Harbor.Hostname, r.Harbor.HTTPPort)
	return endpoint, nil
}

func installAddons(cfg *config.Config, st *clusterState, out io.Writer) error {
	// Use WaitForSSH instead of DialWithKey: the CP may be briefly unreachable
	// right after k3s starts (network stack flap, systemd restarts).
	runner, err := util.WaitForSSH(st.cpIP, 22, config.VMSSHUser, st.keyPath, 2*time.Minute)
	if err != nil {
		return fmt.Errorf("SSH to %s CP: %w", st.spec.Name, err)
	}
	defer runner.Close()

	if err := addons.EnsureHelm(runner, out); err != nil {
		return fmt.Errorf("[%s] Helm: %w", st.spec.Name, err)
	}

	if st.spec.Addons.Cilium.Enabled {
		clusterID, inMesh := cfg.ClusterMeshID(st.spec.Name)
		if err := addons.InstallCilium(runner, st.spec.Addons.Cilium, st.spec.Name, st.cpIP, clusterID, inMesh, out); err != nil {
			return err
		}
	}

	if st.spec.Addons.Monitoring.Enabled {
		if err := addons.InstallMonitoring(runner, st.spec.Addons.Monitoring, st.spec.Name, out); err != nil {
			return err
		}
	}

	if st.spec.Addons.Istio.Enabled {
		if err := addons.InstallIstio(runner, st.spec.Addons.Istio, st.spec.Name, out); err != nil {
			return err
		}
		if st.spec.Addons.Monitoring.Enabled {
			if err := addons.InstallIstioMonitors(runner, st.spec.Name, out); err != nil {
				return err
			}
		}
		if st.spec.Addons.Jaeger.Enabled {
			if err := addons.InstallJaeger(runner, st.spec.Addons.Jaeger, st.spec.Name, out); err != nil {
				return err
			}
		}
		if err := addons.InstallKiali(runner, st.spec.Addons.Kiali, st.spec.Addons.Jaeger.Enabled, st.spec.Name, out); err != nil {
			return err
		}
	}

	if st.spec.Addons.NFS.Enabled {
		ui.Step(out, "[%s] configuring NFS server export...", st.spec.Name)
		if err := ConfigureNFSExportForCluster(
			st.spec.Addons.NFS.Server,
			st.spec.Addons.NFS.DataDir,
			st.spec.Addons.NFS.ExportSubnet,
			st.spec.Name,
			st.keyPath,
			out,
		); err != nil {
			return err
		}
		if err := addons.InstallNFSCSI(runner, st.spec.Addons.NFS.Server, st.spec.Name, st.spec.Addons.NFS.DataDir, st.spec.Addons.NFS, out); err != nil {
			return err
		}
	}

	if st.spec.Addons.ChaosMesh.Enabled {
		if err := addons.InstallChaosMesh(runner, st.spec.Addons.ChaosMesh, st.spec.Name, out); err != nil {
			return err
		}
	}

	if st.spec.Addons.Mentat.Enabled {
		if err := addons.InstallMentat(runner, st.spec.Addons.Mentat, st.spec.Name, st.spec.Addons.Monitoring.Enabled, out); err != nil {
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
		runner, err := util.DialWithKey(st.cpIP, 22, config.VMSSHUser, st.keyPath)
		if err != nil {
			return fmt.Errorf("SSH to %s: %w", st.spec.Name, err)
		}
		err = addons.EnableClusterMesh(runner, st.spec.Name, out)
		runner.Close()
		if err != nil {
			return err
		}
	}

	for i, source := range states {
		dests := make(map[string][]byte)
		for _, dest := range states[i+1:] {
			dests[dest.spec.Name] = dest.kubeconfig
		}
		if len(dests) == 0 {
			continue
		}

		runner, err := util.DialWithKey(source.cpIP, 22, config.VMSSHUser, source.keyPath)
		if err != nil {
			return fmt.Errorf("SSH to %s: %w", source.spec.Name, err)
		}
		err = addons.ConnectClusterMesh(runner, source.spec.Name,
			source.kubeconfig, dests, out)
		runner.Close()
		if err != nil {
			return err
		}
	}

	ui.Success(out, "All clusters connected via Cilium cluster mesh")
	return nil
}

// createSingle provisions VMs and installs k3s for one cluster.
func createSingle(ctx context.Context, cfg *config.Config, vmidBase int, registryEndpoint string, out io.Writer) error {
	keyPath, err := cfg.SSHKeyFilePath()
	if err != nil {
		return fmt.Errorf("SSH key path: %w", err)
	}

	ui.Section(out, "SSH key pair")
	keyPair, err := util.EnsureKeyPairAt(keyPath)
	if err != nil {
		return fmt.Errorf("SSH key pair: %w", err)
	}
	ui.Info(out, "private key: %s", keyPair.PrivateKeyPath)

	ui.Section(out, "Connecting to Proxmox")
	px, err := pxclient.New(cfg)
	if err != nil {
		return err
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
	if registryEndpoint != "" {
		installer.SetRegistryEndpoint(registryEndpoint)
	}

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
		templateName := node.Template
		if templateName == "" {
			templateName = cfg.Template.Name
		}
		specs = append(specs, pxclient.VMSpec{
			TemplateName: templateName,
			VMID:         vmidBase + i,
			Name:         config.PrefixedNodeName(cfg.ClusterName, node.Name),
			ProxmoxNode:  node.ProxmoxNode,
			Cores:        node.Cores,
			Memory:       node.Memory,
			DiskSize:     node.DiskSize,
			Storage:      node.Storage,
			Bridge:       node.Bridge,
			IPAddress:    node.IP,
			Gateway:      node.Gateway,
			DNS:          node.DNS,
			SubnetMask:   node.SubnetMask,
			User:         config.VMSSHUser,
			SSHPubKey:    pubKey,
			ClusterName:  cfg.ClusterName,
			Role:         "server",
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
		templateName := node.Template
		if templateName == "" {
			templateName = cfg.Template.Name
		}
		specs = append(specs, pxclient.VMSpec{
			TemplateName: templateName,
			VMID:         vmidBase + i,
			Name:         config.PrefixedNodeName(cfg.ClusterName, node.Name),
			ProxmoxNode:  node.ProxmoxNode,
			Cores:        node.Cores,
			Memory:       node.Memory,
			DiskSize:     node.DiskSize,
			Storage:      node.Storage,
			Bridge:       node.Bridge,
			IPAddress:    node.IP,
			Gateway:      node.Gateway,
			DNS:          node.DNS,
			SubnetMask:   node.SubnetMask,
			User:         config.VMSSHUser,
			SSHPubKey:    pubKey,
			ClusterName:  cfg.ClusterName,
			Role:         "agent",
			Labels:       node.Labels,
			Taints:       node.Taints,
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
	g, _ := errgroup.WithContext(ctx)
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
