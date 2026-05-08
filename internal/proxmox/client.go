package proxmox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	pxapi "github.com/luthermonson/go-proxmox"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
)

type Client struct {
	api        *pxapi.Client
	cfg        *config.Config
	httpClient *http.Client
}

func New(cfg *config.Config) (*Client, error) {
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: cfg.Proxmox.InsecureTLS,
			},
		},
	}

	api := pxapi.NewClient(cfg.Proxmox.APIURL,
		pxapi.WithHTTPClient(httpClient),
		pxapi.WithAPIToken(cfg.Proxmox.TokenID, cfg.Proxmox.TokenSecret),
	)

	ctx := context.Background()
	if _, err := api.Version(ctx); err != nil {
		return nil, fmt.Errorf("connecting to Proxmox at %s: %w", cfg.Proxmox.APIURL, err)
	}

	return &Client{api: api, cfg: cfg, httpClient: httpClient}, nil
}

// ConfigVM posts a VM config update via raw HTTP because go-proxmox does not
// expose the POST /nodes/{node}/qemu/{vmid}/config endpoint.
func (c *Client) ConfigVM(ctx context.Context, node string, vmid int, formBody string) (*pxapi.Task, error) {
	endpoint := strings.TrimRight(c.cfg.Proxmox.APIURL, "/") + fmt.Sprintf("/nodes/%s/qemu/%d/config", node, vmid)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(formBody))
	if err != nil {
		return nil, fmt.Errorf("building VM config request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "PVEAPIToken="+c.cfg.Proxmox.TokenID+"="+c.cfg.Proxmox.TokenSecret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending VM config request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading VM config response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("bad request: %s - %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	var result struct {
		Data pxapi.UPID `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing VM config response: %w", err)
	}

	return pxapi.NewTask(result.Data, c.api), nil
}

func (c *Client) Node(ctx context.Context, name string) (*pxapi.Node, error) {
	node, err := c.api.Node(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("getting node %q: %w", name, err)
	}
	return node, nil
}

func (c *Client) NextVMID(ctx context.Context, startFrom int) (int, error) {
	nodes, err := c.api.Nodes(ctx)
	if err != nil {
		return 0, fmt.Errorf("listing nodes: %w", err)
	}

	used := make(map[int]bool)
	for _, nodeStatus := range nodes {
		node, err := c.api.Node(ctx, nodeStatus.Node)
		if err != nil {
			continue
		}
		vms, err := node.VirtualMachines(ctx)
		if err != nil {
			continue
		}
		for _, vm := range vms {
			used[int(vm.VMID)] = true
		}
	}

	for id := startFrom; ; id++ {
		if !used[id] {
			return id, nil
		}
	}
}

func (c *Client) FindVMByName(ctx context.Context, name string) (*pxapi.VirtualMachine, error) {
	nodes, err := c.api.Nodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}

	for _, nodeStatus := range nodes {
		node, err := c.api.Node(ctx, nodeStatus.Node)
		if err != nil {
			continue
		}
		vms, err := node.VirtualMachines(ctx)
		if err != nil {
			continue
		}
		for _, vm := range vms {
			if vm.Name == name {
				return vm, nil
			}
		}
	}
	return nil, nil
}

func (c *Client) FindClusterVMs(ctx context.Context, clusterName string) ([]*pxapi.VirtualMachine, error) {
	nodes, err := c.api.Nodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}

	var result []*pxapi.VirtualMachine
	for _, nodeStatus := range nodes {
		node, err := c.api.Node(ctx, nodeStatus.Node)
		if err != nil {
			continue
		}
		vms, err := node.VirtualMachines(ctx)
		if err != nil {
			continue
		}
		for _, vm := range vms {
			if hasTag(vm.Tags, "proxmox-k3s") && hasTag(vm.Tags, clusterName) {
				result = append(result, vm)
			}
		}
	}
	return result, nil
}

func hasTag(tags, target string) bool {
	for _, t := range splitTags(tags) {
		if t == target {
			return true
		}
	}
	return false
}

func splitTags(tags string) []string {
	var out []string
	for _, t := range strings.Split(tags, ";") {
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}
