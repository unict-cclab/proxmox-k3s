package addons

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
	"github.com/unict-cclab/proxmox-k3s/internal/util"
)

const harborVersion = "v2.12.0"

type registryTarget struct {
	projectName string
	upstreamURL string
	harborType  string
	mirrorKey   string
}

// registryTargets lists the upstream registries to proxy through Harbor.
var registryTargets = []registryTarget{
	{"dockerhub-proxy", "https://registry-1.docker.io", "docker-hub", "docker.io"},
	{"k8s-proxy", "https://registry.k8s.io", "docker-registry", "registry.k8s.io"},
	{"ghcr-proxy", "https://ghcr.io", "github-ghcr", "ghcr.io"},
	{"gcr-proxy", "https://gcr.io", "docker-registry", "gcr.io"},
	{"quay-proxy", "https://quay.io", "quay", "quay.io"},
}

// InstallHarbor installs Harbor on the registry VM and creates proxy cache
// projects for the common upstream registries.
func InstallHarbor(runner *util.Runner, harbor config.HarborConfig, out io.Writer) error {
	ui.Step(out, "[harbor] installing Docker...")
	if err := installDockerForHarbor(runner, out); err != nil {
		return fmt.Errorf("harbor: Docker: %w", err)
	}

	ui.Step(out, "[harbor] downloading Harbor %s...", harborVersion)
	if err := downloadHarbor(runner, out); err != nil {
		return fmt.Errorf("harbor: download: %w", err)
	}

	ui.Step(out, "[harbor] configuring Harbor...")
	if err := writeHarborYAML(runner, harbor, out); err != nil {
		return fmt.Errorf("harbor: configure: %w", err)
	}

	ui.Step(out, "[harbor] running installer (pulls images — may take a while)...")
	var installOut bytes.Buffer
	if err := runner.Run("cd /opt/harbor && sudo ./install.sh", &installOut); err != nil {
		_, _ = io.Copy(out, &installOut)
		return fmt.Errorf("harbor: install.sh: %w", err)
	}

	ui.Step(out, "[harbor] waiting for Harbor to be ready...")
	if err := waitForHarbor(runner, harbor, out); err != nil {
		return fmt.Errorf("harbor: readiness: %w", err)
	}

	// Reconnect: the TCP connection may have gone stale after the long image-pull + readiness wait.
	if err := runner.Reconnect(); err != nil {
		return fmt.Errorf("harbor: reconnecting after install: %w", err)
	}

	ui.Step(out, "[harbor] creating proxy cache projects...")
	if err := EnsureProxyProjects(runner, harbor, out); err != nil {
		return fmt.Errorf("harbor: proxy projects: %w", err)
	}

	ui.Success(out, "[harbor] ready at http://%s:%d", harbor.Hostname, harbor.HTTPPort)
	return nil
}

// RegistriesYAML returns the content for /etc/rancher/k3s/registries.yaml
// that routes all pulls through Harbor's proxy cache projects.
func RegistriesYAML(harbor config.HarborConfig) string {
	endpoint := fmt.Sprintf("http://%s:%d", harbor.Hostname, harbor.HTTPPort)
	var sb strings.Builder
	sb.WriteString("mirrors:\n")
	for _, t := range registryTargets {
		fmt.Fprintf(&sb, "  %q:\n", t.mirrorKey)
		fmt.Fprintf(&sb, "    endpoint:\n")
		fmt.Fprintf(&sb, "      - %q\n", endpoint)
		fmt.Fprintf(&sb, "    rewrite:\n")
		fmt.Fprintf(&sb, "      \"^(.*)\": %q\n", t.projectName+"/$1")
	}
	return sb.String()
}

func installDockerForHarbor(runner *util.Runner, out io.Writer) error {
	script := strings.Join([]string{
		"sudo cloud-init status --wait || true",
		"sudo apt-get update -y",
		"sudo apt-get install -y ca-certificates curl jq",
		"sudo install -m 0755 -d /etc/apt/keyrings",
		"sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc",
		"sudo chmod a+r /etc/apt/keyrings/docker.asc",
		`echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null`,
		"sudo apt-get update -y",
		"sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin",
		"sudo systemctl enable --now docker",
		"sudo usermod -aG docker ubuntu",
	}, " && ")
	return runner.Run(script, out)
}

func downloadHarbor(runner *util.Runner, out io.Writer) error {
	script := strings.Join([]string{
		fmt.Sprintf(
			"curl -fsSL -o /tmp/harbor.tgz https://github.com/goharbor/harbor/releases/download/%s/harbor-online-installer-%s.tgz",
			harborVersion, harborVersion,
		),
		"sudo tar xzvf /tmp/harbor.tgz -C /opt",
		"rm /tmp/harbor.tgz",
	}, " && ")
	return runner.Run(script, out)
}

func writeHarborYAML(runner *util.Runner, harbor config.HarborConfig, out io.Writer) error {
	// Patch the bundled harbor.yml.tmpl instead of maintaining our own copy.
	// Values are embedded as Python string literals to avoid shell quoting issues.
	script := fmt.Sprintf(`
import re

hostname   = %q
admin_pw   = %q
data_vol   = %q
http_port  = %d

with open('/opt/harbor/harbor.yml.tmpl') as f:
    lines = f.readlines()

result = []
in_https = False
in_http  = False
for line in lines:
    top_key = re.match(r'^([a-zA-Z_][a-zA-Z0-9_]*):', line)
    if top_key:
        in_https = top_key.group(1) == 'https'
        in_http  = top_key.group(1) == 'http'

    # Comment out the https block (we run HTTP-only).
    if in_https:
        result.append('# ' + line if line.strip() else line)
        continue

    # Patch top-level scalar fields.
    if re.match(r'^hostname:', line):
        result.append('hostname: ' + hostname + '\n')
        continue
    if re.match(r'^harbor_admin_password:', line):
        result.append('harbor_admin_password: ' + admin_pw + '\n')
        continue
    if re.match(r'^data_volume:', line):
        result.append('data_volume: ' + data_vol + '\n')
        continue

    # Patch http port.
    if in_http and re.match(r'\s+port:', line):
        result.append('  port: ' + str(http_port) + '\n')
        continue

    result.append(line)

with open('/opt/harbor/harbor.yml', 'w') as f:
    f.writelines(result)
`, harbor.Hostname, harbor.AdminPassword, harbor.DataVolume, harbor.HTTPPort)

	if err := runner.WriteFile("/tmp/patch_harbor.py", []byte(script)); err != nil {
		return err
	}
	return runner.Run("sudo python3 /tmp/patch_harbor.py && sudo rm /tmp/patch_harbor.py", out)
}

func waitForHarbor(runner *util.Runner, harbor config.HarborConfig, out io.Writer) error {
	// Phase 1: wait for the nginx frontend (ping — no auth required).
	// Phase 2: wait for harbor-core by listing projects with Basic Auth.
	//   /api/v2.0/systeminfo can return Harbor-Secret errors in some setups;
	//   /api/v2.0/projects reliably accepts Basic Auth and requires core to be up.
	pingURL := fmt.Sprintf("http://localhost:%d/api/v2.0/ping", harbor.HTTPPort)
	projectsURL := fmt.Sprintf("http://localhost:%d/api/v2.0/projects", harbor.HTTPPort)
	creds := fmt.Sprintf("admin:%s", harbor.AdminPassword)
	script := fmt.Sprintf(`PING_OK=0
for i in $(seq 1 90); do
  STATUS=$(curl -s -o /dev/null -w '%%{http_code}' %s 2>/dev/null || echo 000)
  if [ "$STATUS" = "200" ]; then PING_OK=1; echo "Harbor ping ready (attempt $i)"; break; fi
  echo "  waiting for Harbor ($i/90, HTTP $STATUS)..."
  sleep 10
done
[ "$PING_OK" = "1" ] || { echo "Harbor did not respond to ping after 90 attempts" >&2; exit 1; }
for i in $(seq 1 60); do
  STATUS=$(curl -s -o /dev/null -w '%%{http_code}' -u '%s' %s 2>/dev/null || echo 000)
  if [ "$STATUS" = "200" ]; then echo "Harbor core ready (attempt $i)"; exit 0; fi
  echo "  waiting for Harbor core ($i/60, HTTP $STATUS)..."
  sleep 5
done
echo "Harbor core did not become ready" >&2; exit 1`,
		pingURL, creds, projectsURL,
	)
	return runner.Run(script, out)
}

func EnsureProxyProjects(runner *util.Runner, harbor config.HarborConfig, out io.Writer) error {
	baseURL := fmt.Sprintf("http://localhost:%d", harbor.HTTPPort)
	creds := fmt.Sprintf("admin:%s", harbor.AdminPassword)

	var script strings.Builder

	for _, t := range registryTargets {
		regName := t.mirrorKey + "-upstream"

		// Create registry endpoint. Tolerate 409 (already exists); fail on anything else.
		fmt.Fprintf(&script, "echo '  [harbor] endpoint: %s'\n", regName)
		fmt.Fprintf(&script,
			"STATUS=$(curl -s -o /dev/null -w '%%{http_code}' -u '%s' -X POST '%s/api/v2.0/registries'"+
				" -H 'Content-Type: application/json'"+
				" -d '{\"name\":\"%s\",\"url\":\"%s\",\"type\":\"%s\"}')\n"+
				"[ \"$STATUS\" = 201 ] || [ \"$STATUS\" = 409 ] || { echo \"  endpoint %s: unexpected HTTP $STATUS\"; exit 1; }\n",
			creds, baseURL,
			regName, t.upstreamURL, t.harborType,
			regName,
		)

		// Fetch the registry ID (required to create a proxy-cache project).
		fmt.Fprintf(&script,
			"REG_ID=$(curl -sf -u '%s' '%s/api/v2.0/registries?name=%s' | jq -r '.[0].id')\n"+
				"[ -n \"$REG_ID\" ] && [ \"$REG_ID\" != null ] ||"+
				" { echo '  could not resolve ID for %s'; exit 1; }\n",
			creds, baseURL, regName,
			regName,
		)

		// Create proxy cache project. The registry_id is a number so it is injected
		// outside the single-quoted JSON literals to avoid shell quoting issues.
		fmt.Fprintf(&script, "echo '  [harbor] project: %s'\n", t.projectName)
		fmt.Fprintf(&script,
			"STATUS=$(curl -s -o /dev/null -w '%%{http_code}' -u '%s' -X POST '%s/api/v2.0/projects'"+
				" -H 'Content-Type: application/json'"+
				" -d '{\"project_name\":\"%s\",\"registry_id\":'\"$REG_ID\"',\"public\":true,\"metadata\":{\"public\":\"true\"}}')\n"+
				"[ \"$STATUS\" = 201 ] || [ \"$STATUS\" = 409 ] || { echo \"  project %s: unexpected HTTP $STATUS\"; exit 1; }\n",
			creds, baseURL,
			t.projectName,
			t.projectName,
		)
	}

	return runner.Run(script.String(), out)
}
