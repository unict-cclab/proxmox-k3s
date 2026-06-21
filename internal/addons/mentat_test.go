package addons

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"gopkg.in/yaml.v3"
)

func TestRenderMentatCoreManifestIncludesNetworkProbeSettings(t *testing.T) {
	manifest := renderMentatCoreManifest(config.MentatConfig{
		Version:                  "v1.2.3",
		SleepSeconds:             7,
		PingAttempts:             4,
		PingTimeoutSeconds:       2,
		BandwidthPort:            3113,
		BandwidthBytes:           1048576,
		BandwidthIntervalSeconds: 45,
		BandwidthTimeoutSeconds:  15,
	})

	for _, expected := range []string{
		"ghcr.io/unict-cclab/mentat:v1.2.3",
		"containerPort: 3113",
		"resources: [\"pods\"]",
		"name: POD_NAMESPACE",
		"name: PING_ATTEMPTS\n          value: \"4\"",
		"name: BANDWIDTH_BYTES\n          value: \"1048576\"",
		"name: BANDWIDTH_INTERVAL_SECONDS\n          value: \"45\"",
	} {
		if !strings.Contains(manifest, expected) {
			t.Errorf("rendered manifest does not contain %q", expected)
		}
	}
	if strings.Contains(manifest, "%!") {
		t.Fatalf("rendered manifest contains a formatting error: %s", manifest)
	}
}

func TestInfrastructureDashboardIncludesMentatNetworkMetrics(t *testing.T) {
	var configMap struct {
		Data map[string]string `yaml:"data"`
	}
	if err := yaml.Unmarshal([]byte(infrastructureGrafanaDashboardManifest), &configMap); err != nil {
		t.Fatalf("parse dashboard ConfigMap: %v", err)
	}

	var dashboard struct {
		Panels []struct {
			Title   string `json:"title"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal([]byte(configMap.Data["sophos-node-dashboard.json"]), &dashboard); err != nil {
		t.Fatalf("parse dashboard JSON: %v", err)
	}

	titles := make(map[string]bool)
	expressions := make([]string, 0)
	for _, panel := range dashboard.Panels {
		titles[panel.Title] = true
		for _, target := range panel.Targets {
			expressions = append(expressions, target.Expr)
		}
	}

	if titles["Node Latency Graph"] {
		t.Error("infrastructure dashboard still contains the node graph")
	}
	for _, title := range []string{"Inter-node Packet Loss", "Available Inter-node Bandwidth"} {
		if !titles[title] {
			t.Errorf("infrastructure dashboard is missing %q", title)
		}
	}
	joinedExpressions := strings.Join(expressions, "\n")
	for _, metric := range []string{"node_packet_loss_ratio", "node_bandwidth_bytes_per_second"} {
		if !strings.Contains(joinedExpressions, metric) {
			t.Errorf("infrastructure dashboard does not query %s", metric)
		}
	}
	if strings.Contains(monitoringValuesTemplate, "volkovlabs-echarts-panel") {
		t.Error("monitoring still installs the unused ECharts panel plugin")
	}
}
