package addons

import (
	"fmt"
	"io"
	"strings"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

const cpaOperatorChartURLTemplate = "https://github.com/jthomperoo/custom-pod-autoscaler-operator/releases/download/%s/custom-pod-autoscaler-operator-%s.tgz"

// InstallCPAOperator installs the Custom Pod Autoscaler operator and CRD.
func InstallCPAOperator(runner *util.Runner, addon config.CPAOperatorConfig, clusterName string, out io.Writer) error {
	chart := fmt.Sprintf(cpaOperatorChartURLTemplate, addon.Version, addon.Version)

	ui.Step(out, "[%s] installing Custom Pod Autoscaler operator %s...", clusterName, addon.Version)
	cmd := strings.Join([]string{
		"helm upgrade --install", addon.Release, chart,
		"--namespace", addon.Namespace,
		"--create-namespace",
		"--wait --timeout 10m",
	}, " ")
	if err := runner.Run(cmd, out); err != nil {
		return fmt.Errorf("[%s] installing Custom Pod Autoscaler operator: %w", clusterName, err)
	}

	ui.Success(out, "[%s] Custom Pod Autoscaler operator ready", clusterName)
	return nil
}
