package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/amarchese96/proxmox-k3s/api"
	"github.com/amarchese96/proxmox-k3s/internal/config"
	"github.com/amarchese96/proxmox-k3s/internal/ui"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	svc := api.New()
	root := &cobra.Command{
		Use:   "proxmox-k3s",
		Short: "Provision and manage k3s clusters on Proxmox VE",
		Long:  `proxmox-k3s creates production-ready k3s clusters on Proxmox VE with a single config file and a single command.`,
	}

	root.AddCommand(
		createCmd(svc),
		deleteCmd(svc),
		kubeconfigCmd(svc),
		templateCmd(svc),
	)
	return root
}

func createCmd(svc *api.Service) *cobra.Command {
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
			return svc.CreateCluster(context.Background(), cfg, os.Stdout)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	return cmd
}

func deleteCmd(svc *api.Service) *cobra.Command {
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

			fmt.Fprintf(os.Stdout, "%s This will DELETE all VMs for cluster %q. Continue? [y/N] ",
				ui.PromptPrefix("warn"), cfg.ClusterName)
			var answer string
			fmt.Scanln(&answer)
			if answer != "y" && answer != "Y" {
				ui.Warn(os.Stdout, "Aborted.")
				return nil
			}

			return svc.DeleteCluster(context.Background(), cfg, deleteTemplate, os.Stdout)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	cmd.Flags().BoolVar(&deleteTemplate, "template", false, "also delete the VM template")
	return cmd
}

func kubeconfigCmd(svc *api.Service) *cobra.Command {
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

			return svc.RefreshKubeconfig(context.Background(), cfg, os.Stdout)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	return cmd
}

func templateCmd(svc *api.Service) *cobra.Command {
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
			return svc.CreateTemplate(context.Background(), cfg, os.Stdout)
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
			return svc.DeleteTemplate(context.Background(), cfg, os.Stdout)
		},
	}

	createSub.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	deleteSub.Flags().StringVarP(&configPath, "config", "c", "cluster.yaml", "path to cluster config file")
	cmd.AddCommand(createSub, deleteSub)
	return cmd
}
