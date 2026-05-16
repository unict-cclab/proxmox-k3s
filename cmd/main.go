package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/unict-cclab/proxmox-k3s/internal/cluster"
	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/k3s"
	pxclient "github.com/unict-cclab/proxmox-k3s/internal/proxmox"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

// version is set at build time via -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "proxmox-k3s",
		Short:   "Provision and manage k3s clusters on Proxmox VE",
		Version: version,
	}

	root.AddCommand(
		setupAllCmd(),
		deleteAllCmd(),
		clusterCmd(),
		templateCmd(),
		registryCmd(),
		nfsCmd(),
	)
	return root
}

func setupAllCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "setup-all",
		Short: "Provision all resources",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			return cluster.Setup(context.Background(), cfg, os.Stdout)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	return cmd
}

func deleteAllCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "delete-all",
		Short: "Tear down all resources",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "%s This will DELETE all clusters, the registry, and the template. Continue? [y/N] ",
				ui.PromptPrefix("warn"))
			var answer string
			fmt.Scanln(&answer)
			if answer != "y" && answer != "Y" {
				ui.Warn(os.Stdout, "Aborted.")
				return nil
			}
			return cluster.Teardown(context.Background(), cfg, os.Stdout)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	return cmd
}

func clusterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Manage k3s clusters",
	}
	cmd.AddCommand(createCmd(), deleteCmd(), kubeconfigCmd(), clusterMeshCmd())
	return cmd
}

func createCmd() *cobra.Command {
	var (
		configPath   string
		clusterNames []string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a k3s cluster",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			if len(clusterNames) > 0 {
				var filtered []config.ClusterSpec
				for _, name := range clusterNames {
					found := false
					for _, spec := range cfg.Clusters {
						if spec.Name == name {
							filtered = append(filtered, spec)
							found = true
							break
						}
					}
					if !found {
						return fmt.Errorf("cluster %q not found in config", name)
					}
				}
				cfg.Clusters = filtered
			}
			return cluster.Create(context.Background(), cfg, os.Stdout)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	cmd.Flags().StringArrayVar(&clusterNames, "cluster", nil, "cluster name to create (repeatable; default: all)")
	return cmd
}

func deleteCmd() *cobra.Command {
	var (
		configPath   string
		clusterNames []string
	)

	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete k3s cluster VMs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			if len(clusterNames) > 0 {
				var filtered []config.ClusterSpec
				for _, name := range clusterNames {
					found := false
					for _, spec := range cfg.Clusters {
						if spec.Name == name {
							filtered = append(filtered, spec)
							found = true
							break
						}
					}
					if !found {
						return fmt.Errorf("cluster %q not found in config", name)
					}
				}
				cfg.Clusters = filtered
			}

			target := cfg.Clusters[0].Name
			if len(cfg.Clusters) > 1 {
				target = fmt.Sprintf("%d clusters", len(cfg.Clusters))
			}
			fmt.Fprintf(os.Stdout, "%s This will DELETE all VMs for %s. Continue? [y/N] ",
				ui.PromptPrefix("warn"), target)
			var answer string
			fmt.Scanln(&answer)
			if answer != "y" && answer != "Y" {
				ui.Warn(os.Stdout, "Aborted.")
				return nil
			}

			return cluster.Delete(context.Background(), cfg, os.Stdout)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	cmd.Flags().StringArrayVar(&clusterNames, "cluster", nil, "cluster name to delete (repeatable; default: all)")
	return cmd
}

func kubeconfigCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "kubeconfig",
		Short: "Re-fetch and save the cluster kubeconfig",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			clusterName, _ := cmd.Flags().GetString("cluster")
			spec := cfg.Clusters[0]
			for _, s := range cfg.Clusters {
				if s.Name == clusterName {
					spec = s
					break
				}
			}

			keyPath, err := cfg.SSHKeyFilePath()
			if err != nil {
				return err
			}
			keyPair, err := util.EnsureKeyPairAt(keyPath)
			if err != nil {
				return err
			}

			px, err := pxclient.New(cfg)
			if err != nil {
				return err
			}

			cpName := config.PrefixedNodeName(spec.Name, spec.ControlPlane[0].Name)
			vm, err := px.FindVMByName(context.Background(), cpName)
			if err != nil || vm == nil {
				return fmt.Errorf("control-plane VM %q not found; has the cluster been created?", cpName)
			}

			ip := spec.ControlPlane[0].IP
			if ip == "" {
				ip, err = pxclient.WaitForIP(context.Background(), vm, 30*time.Second)
				if err != nil {
					return fmt.Errorf("getting CP IP: %w", err)
				}
			}

			runner, err := util.DialWithKey(ip, 22, config.VMSSHUser, keyPair.PrivateKeyPath)
			if err != nil {
				return fmt.Errorf("SSH to %s: %w", ip, err)
			}
			defer runner.Close()

			raw, err := runner.Output("sudo cat /etc/rancher/k3s/k3s.yaml")
			if err != nil {
				return fmt.Errorf("reading kubeconfig: %w", err)
			}
			if raw == "" {
				return fmt.Errorf("reading kubeconfig: empty kubeconfig")
			}

			kubeconfig := k3s.RewriteKubeconfig(raw, ip, spec.Name)
			if kubeconfig == "" {
				return fmt.Errorf("rewriting kubeconfig: empty kubeconfig")
			}
			if err := os.WriteFile(spec.KubeconfigPath, []byte(kubeconfig), 0600); err != nil {
				return fmt.Errorf("writing kubeconfig: %w", err)
			}

			fmt.Fprintf(os.Stdout, "Kubeconfig saved to %s\n", spec.KubeconfigPath)
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	cmd.Flags().String("cluster", "", "cluster name to fetch kubeconfig for (default: first cluster)")
	return cmd
}

func templateCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "template",
		Short: "Manage the VM template",
	}

	createSub := &cobra.Command{
		Use:   "create",
		Short: "Build the VM template (runs automatically during setup-all)",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			keyPath, err := cfg.SSHKeyFilePath()
			if err != nil {
				return err
			}
			keyPair, err := util.EnsureKeyPairAt(keyPath)
			if err != nil {
				return err
			}
			px, err := pxclient.New(cfg)
			if err != nil {
				return err
			}
			return pxclient.EnsureTemplate(context.Background(), px, cfg, keyPair.PrivateKeyPath, keyPair.PublicKey, os.Stdout)
		},
	}

	deleteSub := &cobra.Command{
		Use:   "delete",
		Short: "Remove the VM template",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			px, err := pxclient.New(cfg)
			if err != nil {
				return err
			}
			return pxclient.DeleteTemplate(context.Background(), px, cfg, os.Stdout)
		},
	}

	createSub.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	deleteSub.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	cmd.AddCommand(createSub, deleteSub)
	return cmd
}

func registryCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Manage the Harbor pull-through cache registry VM",
	}

	createSub := &cobra.Command{
		Use:   "create",
		Short: "Provision the Harbor registry VM (runs automatically during setup-all)",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			if cfg.Registry == nil {
				return fmt.Errorf("no registry block found in config")
			}
			endpoint, err := cluster.CreateRegistry(context.Background(), cfg, os.Stdout)
			if err != nil {
				return err
			}
			ui.Success(os.Stdout, "Registry ready at %s", endpoint)
			return nil
		},
	}

	deleteSub := &cobra.Command{
		Use:   "delete",
		Short: "Remove the Harbor registry VM",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			if cfg.Registry == nil {
				return fmt.Errorf("no registry block found in config")
			}
			fmt.Fprintf(os.Stdout, "%s This will DELETE the registry VM %q. Continue? [y/N] ",
				ui.PromptPrefix("warn"), cfg.Registry.VM.Name)
			var answer string
			fmt.Scanln(&answer)
			if answer != "y" && answer != "Y" {
				ui.Warn(os.Stdout, "Aborted.")
				return nil
			}
			return cluster.DeleteRegistry(context.Background(), cfg, os.Stdout)
		},
	}

	createSub.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	deleteSub.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	cmd.AddCommand(createSub, deleteSub)
	return cmd
}

func nfsCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "nfs",
		Short: "Manage the NFS server VM",
	}

	createSub := &cobra.Command{
		Use:   "create",
		Short: "Provision the NFS server VM (runs automatically during setup-all)",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			if cfg.NFS == nil {
				return fmt.Errorf("no nfs block found in config")
			}
			return cluster.CreateNFSServer(context.Background(), cfg, os.Stdout)
		},
	}

	deleteSub := &cobra.Command{
		Use:   "delete",
		Short: "Remove the NFS server VM",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			if cfg.NFS == nil {
				return fmt.Errorf("no nfs block found in config")
			}
			fmt.Fprintf(os.Stdout, "%s This will DELETE the NFS server VM %q. Continue? [y/N] ",
				ui.PromptPrefix("warn"), cfg.NFS.VM.Name)
			var answer string
			fmt.Scanln(&answer)
			if answer != "y" && answer != "Y" {
				ui.Warn(os.Stdout, "Aborted.")
				return nil
			}
			return cluster.DeleteNFSServer(context.Background(), cfg, os.Stdout)
		},
	}

	createSub.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	deleteSub.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	cmd.AddCommand(createSub, deleteSub)
	return cmd
}

func clusterMeshCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "mesh",
		Short: "Connect already-running clusters into a Cilium cluster mesh",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			return cluster.SetupMesh(context.Background(), cfg, os.Stdout)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	return cmd
}
