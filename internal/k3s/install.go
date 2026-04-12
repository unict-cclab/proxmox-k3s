package k3s

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/amarchese96/proxmox-k3s/internal/config"
	"github.com/amarchese96/proxmox-k3s/internal/ui"
	"github.com/amarchese96/proxmox-k3s/internal/util"
)

const (
	k3sInstallScript  = "https://get.k3s.io"
	defaultSSHUser    = "ubuntu"
	sshBootTimeout    = 5 * time.Minute
	joinTokenTimeout  = 2 * time.Minute
	kubeconfigTimeout = 2 * time.Minute
	k3sTokenPath      = "/var/lib/rancher/k3s/server/token"
	k3sNodeTokenPath  = "/var/lib/rancher/k3s/server/node-token"
	k3sKubeconfigPath = "/etc/rancher/k3s/k3s.yaml"
)

type NodeInfo struct {
	IP     string
	Name   string
	Runner *util.Runner
}

type Installer struct {
	cfg        *config.Config
	keyPath    string
	sshUser    string
	k3sVersion string
	out        io.Writer
}

func New(cfg *config.Config, keyPath string, out io.Writer) *Installer {
	return &Installer{
		cfg:        cfg,
		keyPath:    keyPath,
		sshUser:    defaultSSHUser,
		k3sVersion: cfg.K3s.Version,
		out:        out,
	}
}

func (i *Installer) ConnectNode(ip, name string) (*NodeInfo, error) {
	ui.Step(i.out, "waiting for SSH on %s (%s)...", name, ip)
	runner, err := util.WaitForSSH(ip, 22, i.sshUser, i.keyPath, sshBootTimeout)
	if err != nil {
		return nil, fmt.Errorf("SSH to %s: %w", name, err)
	}
	return &NodeInfo{IP: ip, Name: name, Runner: runner}, nil
}

func (i *Installer) InstallFirstServer(node *NodeInfo, ha bool) (token string, err error) {
	ui.Step(i.out, "installing k3s server on %s...", node.Name)
	if err := i.ensureServerRole(node); err != nil {
		return "", err
	}

	if err := node.Runner.Run(i.installCommand("server", i.serverArgs(ha, "", "")), i.out); err != nil {
		return "", fmt.Errorf("k3s install on %s: %w", node.Name, err)
	}

	ui.Step(i.out, "waiting for k3s to be ready on %s...", node.Name)
	if err := i.waitForK3sReady(node.Runner); err != nil {
		return "", err
	}

	token, err = i.waitForJoinToken(node)
	if err != nil {
		return "", fmt.Errorf("reading node token from %s: %w", node.Name, err)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("reading node token from %s: empty token", node.Name)
	}
	return token, nil
}

func (i *Installer) InstallAdditionalServer(node *NodeInfo, serverURL, token string) error {
	ui.Step(i.out, "joining server %s to cluster...", node.Name)
	if err := i.ensureServerRole(node); err != nil {
		return err
	}
	if err := node.Runner.Run(i.installCommand("server", i.serverArgs(false, serverURL, token)), i.out); err != nil {
		return fmt.Errorf("k3s install on %s: %w", node.Name, err)
	}
	return i.waitForK3sReady(node.Runner)
}

func (i *Installer) InstallAgent(node *NodeInfo, serverURL, token string) error {
	ui.Step(i.out, "joining agent %s to cluster...", node.Name)
	if err := i.ensureAgentRole(node); err != nil {
		return err
	}

	args := []string{"K3S_URL=" + serverURL, "K3S_TOKEN=" + token}
	if i.cfg.K3s.ExtraAgentArgs != "" {
		args = append(args, "INSTALL_K3S_EXEC="+i.cfg.K3s.ExtraAgentArgs)
	}

	if err := node.Runner.Run(i.installCommand("agent", args), i.out); err != nil {
		return fmt.Errorf("k3s agent install on %s: %w", node.Name, err)
	}
	return nil
}

func (i *Installer) ApplyNodeLabels(cpNode *NodeInfo, nodeName string, labels, taints []string) error {
	if len(labels) == 0 && len(taints) == 0 {
		return nil
	}

	if len(labels) > 0 {
		cmd := fmt.Sprintf("sudo kubectl label node %s %s --overwrite", nodeName, strings.Join(labels, " "))
		if _, err := cpNode.Runner.Output(cmd); err != nil {
			return fmt.Errorf("labeling %s: %w", nodeName, err)
		}
	}

	for _, taint := range taints {
		cmd := fmt.Sprintf("sudo kubectl taint node %s %s --overwrite", nodeName, taint)
		if _, err := cpNode.Runner.Output(cmd); err != nil {
			return fmt.Errorf("tainting %s: %w", nodeName, err)
		}
	}
	return nil
}

func (i *Installer) FetchKubeconfig(node *NodeInfo, externalIP, clusterName string) (string, error) {
	raw, err := i.waitForKubeconfig(node)
	if err != nil {
		return "", fmt.Errorf("reading kubeconfig from %s: %w", node.Name, err)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("reading kubeconfig from %s: empty kubeconfig", node.Name)
	}

	kc := strings.ReplaceAll(raw, "https://127.0.0.1:6443", "https://"+externalIP+":6443")
	kc = strings.ReplaceAll(kc, "name: default", "name: "+clusterName)
	kc = strings.ReplaceAll(kc, "cluster: default", "cluster: "+clusterName)
	kc = strings.ReplaceAll(kc, "user: default", "user: "+clusterName)
	kc = strings.ReplaceAll(kc, "current-context: default", "current-context: "+clusterName)
	if strings.TrimSpace(kc) == "" {
		return "", fmt.Errorf("rewriting kubeconfig for %s produced empty output", node.Name)
	}
	return kc, nil
}

func LatestK3sVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/k3s-io/k3s/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching latest k3s version: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	tag := extractJSONString(string(body), "tag_name")
	if tag == "" {
		return "", fmt.Errorf("could not parse tag_name from GitHub response")
	}
	return tag, nil
}

func (i *Installer) installCommand(role string, envVars []string) string {
	var b strings.Builder
	for _, e := range envVars {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			b.WriteString(parts[0])
			b.WriteString("=")
			b.WriteString(strconv.Quote(parts[1]))
		} else {
			b.WriteString(strconv.Quote(e))
		}
		b.WriteString(" ")
	}
	if i.k3sVersion != "" {
		b.WriteString("INSTALL_K3S_VERSION=")
		b.WriteString(strconv.Quote(i.k3sVersion))
		b.WriteString(" ")
	}
	b.WriteString("sh -s - ")
	b.WriteString(role)
	return fmt.Sprintf("curl -sfL %s | %s", k3sInstallScript, b.String())
}

func (i *Installer) serverArgs(clusterInit bool, serverURL, token string) []string {
	var args []string
	if token != "" {
		args = append(args, "K3S_TOKEN="+token)
	}

	var execArgs []string
	if clusterInit {
		execArgs = append(execArgs, "--cluster-init")
	}
	if serverURL != "" {
		execArgs = append(execArgs, "--server", serverURL)
	}
	if i.cfg.K3s.ExtraServerArgs != "" {
		execArgs = append(execArgs, i.cfg.K3s.ExtraServerArgs)
	}
	if len(execArgs) > 0 {
		args = append(args, "INSTALL_K3S_EXEC="+strings.Join(execArgs, " "))
	}
	return args
}

func (i *Installer) waitForK3sReady(runner *util.Runner) error {
	for attempt := 0; attempt < 24; attempt++ {
		if _, err := runner.Output("sudo kubectl get nodes"); err == nil {
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("k3s did not become ready within 2 minutes")
}

func extractJSONString(body, key string) string {
	search := `"` + key + `":"`
	idx := strings.Index(body, search)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(search):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func (i *Installer) readJoinToken(node *NodeInfo) (string, error) {
	cmd := fmt.Sprintf(`if sudo test -s %s; then sudo cat %s; elif sudo test -s %s; then sudo cat %s; fi`,
		k3sTokenPath, k3sTokenPath, k3sNodeTokenPath, k3sNodeTokenPath)
	return node.Runner.Output(cmd)
}

func (i *Installer) waitForJoinToken(node *NodeInfo) (string, error) {
	deadline := time.Now().Add(joinTokenTimeout)
	var lastErr error

	for time.Now().Before(deadline) {
		token, err := i.readJoinToken(node)
		if err == nil {
			token = strings.TrimSpace(token)
			if token != "" {
				return token, nil
			}
		} else {
			lastErr = err
		}
		time.Sleep(5 * time.Second)
	}

	if lastErr != nil {
		return "", fmt.Errorf("join token not available after %s; last error: %w", joinTokenTimeout, lastErr)
	}
	return "", fmt.Errorf("join token not available after %s", joinTokenTimeout)
}

func (i *Installer) waitForKubeconfig(node *NodeInfo) (string, error) {
	deadline := time.Now().Add(kubeconfigTimeout)
	var lastErr error

	for time.Now().Before(deadline) {
		raw, err := node.Runner.Output(fmt.Sprintf(`if sudo test -s %s; then sudo cat %s; fi`, k3sKubeconfigPath, k3sKubeconfigPath))
		if err == nil {
			raw = strings.TrimSpace(raw)
			if raw != "" {
				return raw, nil
			}
		} else {
			lastErr = err
		}
		time.Sleep(5 * time.Second)
	}

	if lastErr != nil {
		return "", fmt.Errorf("kubeconfig not available after %s; last error: %w", kubeconfigTimeout, lastErr)
	}
	return "", fmt.Errorf("kubeconfig not available after %s", kubeconfigTimeout)
}

func (i *Installer) ensureServerRole(node *NodeInfo) error {
	if _, err := node.Runner.Output(`if [ -x /usr/local/bin/k3s-agent-uninstall.sh ]; then sudo /usr/local/bin/k3s-agent-uninstall.sh; fi`); err != nil {
		return fmt.Errorf("cleaning stale k3s-agent install on %s: %w", node.Name, err)
	}
	return nil
}

func (i *Installer) ensureAgentRole(node *NodeInfo) error {
	if _, err := node.Runner.Output(`if [ -x /usr/local/bin/k3s-uninstall.sh ]; then sudo /usr/local/bin/k3s-uninstall.sh; fi`); err != nil {
		return fmt.Errorf("cleaning stale k3s server install on %s: %w", node.Name, err)
	}
	return nil
}
