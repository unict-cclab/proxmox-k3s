package cluster

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	pxclient "github.com/unict-cclab/proxmox-k3s/internal/proxmox"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

// CreateNFSServer provisions the NFS server VM and configures one export per cluster.
// Idempotent: if the VM already exists the exports are refreshed without reprovisioning.
func CreateNFSServer(ctx context.Context, cfg *config.Config, out io.Writer) error {
	n := cfg.NFS

	keyPath, err := cfg.SSHKeyFilePath()
	if err != nil {
		return fmt.Errorf("SSH key path: %w", err)
	}
	keyPair, err := util.EnsureKeyPairAt(keyPath)
	if err != nil {
		return fmt.Errorf("SSH key pair: %w", err)
	}
	px, err := pxclient.New(cfg)
	if err != nil {
		return err
	}

	if err := checkTemplateExists(ctx, px, cfg); err != nil {
		return err
	}

	ui.Section(out, "=== NFS Server VM ===")

	existing, _ := px.FindVMByName(ctx, n.VM.Name)
	if existing != nil {
		ui.Info(out, "NFS server VM %q already exists, refreshing exports...", n.VM.Name)
		runner, err := util.DialWithKey(n.VM.IP, 22, config.VMSSHUser, keyPair.PrivateKeyPath)
		if err != nil {
			return fmt.Errorf("SSH to NFS server: %w", err)
		}
		defer runner.Close()
		return configureNFSExports(runner, n, cfg.Clusters, out)
	}

	vmid, err := px.NextVMID(ctx, cfg.Template.VMIDBase+1)
	if err != nil {
		return fmt.Errorf("allocating NFS server VMID: %w", err)
	}

	spec := pxclient.VMSpec{
		VMID:        vmid,
		Name:        n.VM.Name,
		ProxmoxNode: n.VM.ProxmoxNode,
		Cores:       n.VM.Cores,
		Memory:      n.VM.Memory,
		DiskSize:    n.VM.DiskSize,
		Storage:     n.VM.Storage,
		Bridge:      n.VM.Bridge,
		IPAddress:   n.VM.IP,
		Gateway:     n.VM.Gateway,
		DNS:         n.VM.DNS,
		SubnetMask:  n.VM.SubnetMask,
		User:        config.VMSSHUser,
		SSHPubKey:   keyPair.PublicKey,
		ClusterName: "nfs",
		Role:        "nfs",
	}

	if _, err := pxclient.CreateVM(ctx, px, cfg, spec, out); err != nil {
		return fmt.Errorf("NFS server VM: %w", err)
	}

	runner, err := util.WaitForSSH(n.VM.IP, 22, config.VMSSHUser, keyPair.PrivateKeyPath, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("SSH to NFS server: %w", err)
	}
	defer runner.Close()

	ui.Step(out, "installing nfs-kernel-server...")
	if err := runner.Run("sudo apt-get update -q && sudo DEBIAN_FRONTEND=noninteractive apt-get install -y nfs-kernel-server", out); err != nil {
		return fmt.Errorf("installing NFS server: %w", err)
	}

	if err := configureNFSExports(runner, n, cfg.Clusters, out); err != nil {
		return err
	}

	ui.Success(out, "NFS server ready at %s", n.VM.IP)
	return nil
}

// DeleteNFSServer removes the NFS server VM.
func DeleteNFSServer(ctx context.Context, cfg *config.Config, out io.Writer) error {
	px, err := pxclient.New(cfg)
	if err != nil {
		return err
	}
	return pxclient.DeleteVM(ctx, px, cfg.NFS.VM.Name, out)
}

// ConfigureNFSExportForCluster SSHes into a pre-existing NFS server, creates
// the cluster's export directory, appends its /etc/exports entry (idempotent),
// and reloads the export table. Called from cluster create when nfs.enabled.
func ConfigureNFSExportForCluster(nfsServer, dataDir, exportSubnet, clusterName, keyPath string, out io.Writer) error {
	runner, err := util.WaitForSSH(nfsServer, 22, config.VMSSHUser, keyPath, 30*time.Second)
	if err != nil {
		return fmt.Errorf("SSH to NFS server %s: %w", nfsServer, err)
	}
	defer runner.Close()

	dir := dataDir + "/" + clusterName
	if _, err := runner.Output(fmt.Sprintf("sudo mkdir -p %s && sudo chmod 777 %s", dir, dir)); err != nil {
		return fmt.Errorf("creating NFS directory %s: %w", dir, err)
	}

	line := fmt.Sprintf("%s %s(rw,sync,no_subtree_check,no_root_squash)", dir, exportSubnet)
	addCmd := fmt.Sprintf(
		"grep -qF %q /etc/exports || echo %q | sudo tee -a /etc/exports > /dev/null",
		dir, line,
	)
	if _, err := runner.Output(addCmd); err != nil {
		return fmt.Errorf("adding NFS export for %s: %w", clusterName, err)
	}

	if err := runner.Run("sudo exportfs -rav", out); err != nil { //nolint:misspell
		return fmt.Errorf("reloading NFS exports: %w", err)
	}
	return nil
}

// configureNFSExports writes /etc/exports with one entry per cluster and reloads.
func configureNFSExports(runner *util.Runner, n *config.NFSConfig, clusters []config.ClusterSpec, out io.Writer) error {
	if len(clusters) == 0 {
		ui.Info(out, "no clusters defined — skipping NFS export configuration")
		return nil
	}
	ui.Step(out, "configuring NFS exports...")

	var dirs []string
	var lines []string
	for _, spec := range clusters {
		dir := n.DataDir + "/" + spec.Name
		dirs = append(dirs, dir)
		lines = append(lines, fmt.Sprintf("%s %s(rw,sync,no_subtree_check,no_root_squash)", dir, n.ExportSubnet))
	}

	// Create all cluster subdirectories.
	mkdirCmd := fmt.Sprintf("sudo mkdir -p %s && sudo chmod 777 %s",
		strings.Join(dirs, " "), strings.Join(dirs, " "))
	if _, err := runner.Output(mkdirCmd); err != nil {
		return fmt.Errorf("creating NFS data directories: %w", err)
	}

	// Write /etc/exports via a temp file.
	exports := strings.Join(lines, "\n") + "\n"
	if err := runner.WriteFile("/tmp/nfs-exports", []byte(exports)); err != nil {
		return fmt.Errorf("writing exports file: %w", err)
	}
	if _, err := runner.Output("sudo cp /tmp/nfs-exports /etc/exports"); err != nil {
		return fmt.Errorf("installing /etc/exports: %w", err)
	}

	if err := runner.Run("sudo exportfs -rav && sudo systemctl enable --now nfs-kernel-server", out); err != nil { //nolint:misspell
		return fmt.Errorf("reloading NFS exports: %w", err)
	}
	return nil
}
