package proxmox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	pxapi "github.com/luthermonson/go-proxmox"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
)

func TestConfigVMUsesFormEncodedBody(t *testing.T) {
	t.Parallel()

	var (
		gotBody string
		gotAuth string
	)

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/nodes/pve/qemu/8001/config":
			body, _ := io.ReadAll(req.Body)
			gotBody = string(body)
			gotAuth = req.Header.Get("Authorization")
			return jsonResponse(http.StatusOK, `{"data":"UPID:pve:00000001:00000001:00000001:config:8001:root@pam:"}`), nil
		case "/nodes/pve/tasks/UPID:pve:00000001:00000001:00000001:config:8001:root@pam:/status":
			return jsonResponse(http.StatusOK, `{"data":{"status":"stopped","exitstatus":"OK"}}`), nil
		default:
			t.Fatalf("unexpected path: %s", req.URL.Path)
			return nil, nil
		}
	})

	cfg := &config.Config{
		Proxmox: config.ProxmoxConfig{
			APIURL:      "https://proxmox.test",
			TokenID:     "root@pam!iac",
			TokenSecret: "secret",
		},
	}

	client := &Client{
		api:        pxapi.NewClient(cfg.Proxmox.APIURL, pxapi.WithHTTPClient(&http.Client{Transport: transport}), pxapi.WithAPIToken(cfg.Proxmox.TokenID, cfg.Proxmox.TokenSecret)),
		cfg:        cfg,
		httpClient: &http.Client{Transport: transport},
	}

	values := encodeConfigBody(map[string]string{
		"ciuser":  "ubuntu",
		"sshkeys": encodeSSHKey("ssh-ed25519 AAAATEST"),
	})

	task, err := client.ConfigVM(context.Background(), "pve", 8001, values)
	if err != nil {
		t.Fatalf("ConfigVM(): %v", err)
	}
	if task.UPID == "" {
		t.Fatalf("expected non-empty task upid")
	}

	if !strings.Contains(gotBody, "ciuser=ubuntu") {
		t.Fatalf("expected ciuser in form body, got %q", gotBody)
	}
	if !strings.Contains(gotBody, "sshkeys=ssh-ed25519%2520AAAATEST") {
		t.Fatalf("expected double-encoded sshkeys in form body, got %q", gotBody)
	}
	if gotAuth != "PVEAPIToken=root@pam!iac=secret" {
		t.Fatalf("unexpected authorization header: %q", gotAuth)
	}
}

func TestTaskLogSummaryUsesRecentNonEmptyLines(t *testing.T) {
	t.Parallel()

	summary := taskLogSummary(pxapi.Log{
		1: "  first line  ",
		2: "",
		3: "update VM 8001",
		4: "TASK ERROR: rbd create 'vm-8001-cloudinit' error: rbd: create error: (17) File exists",
	})

	if !strings.Contains(summary, "vm-8001-cloudinit") {
		t.Fatalf("expected task error in summary, got %q", summary)
	}
	if strings.Contains(summary, "  ") {
		t.Fatalf("expected summary lines to be trimmed, got %q", summary)
	}
}

func TestCloneOptionsStorageOverride(t *testing.T) {
	t.Parallel()

	opts := cloneOptions(VMSpec{
		Name:        "worker-01",
		ProxmoxNode: "pve",
		Storage:     "fast-ssd",
	}, 8101)

	if opts.Storage != "fast-ssd" {
		t.Fatalf("expected storage override, got %q", opts.Storage)
	}
	if opts.NewID != 8101 || opts.Name != "worker-01" || opts.Target != "pve" || opts.Full != 1 {
		t.Fatalf("unexpected clone options: %+v", opts)
	}
}

func TestCloneOptionsOmitsStorageWhenUnset(t *testing.T) {
	t.Parallel()

	opts := cloneOptions(VMSpec{
		Name:        "worker-01",
		ProxmoxNode: "pve",
	}, 8101)

	if opts.Storage != "" {
		t.Fatalf("expected empty storage to let Proxmox use clone defaults, got %q", opts.Storage)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}
