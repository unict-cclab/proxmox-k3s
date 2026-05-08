package addons

import (
	"fmt"
	"io"
	"strings"

	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

// EnsureHelm installs Helm 3 on the remote node if not already present.
func EnsureHelm(runner *util.Runner, out io.Writer) error {
	if _, err := runner.Output("helm version --short 2>/dev/null"); err == nil {
		return nil
	}
	if err := runner.Run("curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash", out); err != nil {
		return fmt.Errorf("installing Helm: %w", err)
	}
	return nil
}

// helmAddRepo adds a Helm repo and updates the cache (idempotent).
func helmAddRepo(runner *util.Runner, name, url string) error {
	cmd := fmt.Sprintf("helm repo add %s %s 2>/dev/null || true && helm repo update", name, url)
	if _, err := runner.Output(cmd); err != nil {
		return fmt.Errorf("adding Helm repo %s: %w", name, err)
	}
	return nil
}

// helmInstall runs `helm upgrade --install` using a values YAML written to a
// temporary file on the remote node (avoids shell-quoting issues with --set).
func helmInstall(runner *util.Runner, release, chart, namespace, valuesYAML string, out io.Writer) error {
	valuesPath := fmt.Sprintf("/tmp/helm-%s-values.yaml", release)
	if err := runner.WriteFile(valuesPath, []byte(valuesYAML)); err != nil {
		return fmt.Errorf("uploading values for %s: %w", release, err)
	}

	cmd := strings.Join([]string{
		"helm upgrade --install", release, chart,
		"--namespace", namespace,
		"--create-namespace",
		"--values", valuesPath,
		"--wait --timeout 10m",
	}, " ")

	if err := runner.Run(cmd, out); err != nil {
		return fmt.Errorf("helm install %s: %w", release, err)
	}
	return nil
}
