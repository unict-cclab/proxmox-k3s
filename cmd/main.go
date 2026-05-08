package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/unict-cclab/proxmox-k3s/internal/cluster"
	"github.com/unict-cclab/proxmox-k3s/internal/config"
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
		Long:    `proxmox-k3s creates production-ready k3s clusters on Proxmox VE with a single config file and a single command.`,
		Version: version,
	}

	root.AddCommand(
		createCmd(),
		deleteCmd(),
		kubeconfigCmd(),
		templateCmd(),
	)
	return root
}

func createCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a k3s cluster",
		Long: `Provisions VMs on Proxmox, installs k3s, and writes a kubeconfig.

Idempotent: already-existing VMs and an already-running k3s installation are
detected and skipped, so re-running after a partial failure is safe.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			return cluster.Create(context.Background(), cfg, os.Stdout)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	return cmd
}

func deleteCmd() *cobra.Command {
	var (
		configPath     string
		deleteTemplate bool
	)

	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a k3s cluster and its VMs",
		Long: `Stops and removes all VMs belonging to the cluster (identified via
Proxmox tags). The VM template is kept by default so re-creating the cluster
is fast; pass --template to remove it too.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			target := cfg.Clusters[0].ClusterName
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

			return cluster.Delete(context.Background(), cfg, deleteTemplate, os.Stdout)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	cmd.Flags().BoolVar(&deleteTemplate, "template", false, "also delete the VM template")
	return cmd
}

func kubeconfigCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "kubeconfig",
		Short: "Re-fetch and save the cluster kubeconfig",
		Long:  `Connects to the first control-plane node and refreshes the kubeconfig file.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			clusterName, _ := cmd.Flags().GetString("cluster")
			spec := cfg.Clusters[0]
			for _, s := range cfg.Clusters {
				if s.ClusterName == clusterName {
					spec = s
					break
				}
			}

			stateDir, err := config.StateDirForCluster(spec.ClusterName)
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

			cpName := config.PrefixedNodeName(spec.ClusterName, spec.ControlPlane[0].Name)
			vm, err := px.FindVMByName(context.Background(), cpName)
			if err != nil || vm == nil {
				return fmt.Errorf("control-plane VM %q not found; has the cluster been created?", cpName)
			}

			ip := spec.ControlPlane[0].IP
			if ip == "" {
				ip, err = pxclient.WaitForIP(context.Background(), vm, 30e9)
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
			if raw == "" {
				return fmt.Errorf("reading kubeconfig: empty kubeconfig")
			}

			kubeconfig := rewriteKubeconfig(raw, ip, spec.ClusterName)
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
		Short: "Build the VM template (runs automatically during cluster create)",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			stateDir, err := config.StateDirForCluster(cfg.Clusters[0].ClusterName)
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

func rewriteKubeconfig(raw, ip, clusterName string) string {
	r := strings.ReplaceAll(raw, "https://127.0.0.1:6443", "https://"+ip+":6443")
	r = strings.ReplaceAll(r, "name: default", "name: "+clusterName)
	r = strings.ReplaceAll(r, "cluster: default", "cluster: "+clusterName)
	r = strings.ReplaceAll(r, "user: default", "user: "+clusterName)
	r = strings.ReplaceAll(r, "current-context: default", "current-context: "+clusterName)
	return r
}
