package proxmox

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	pxapi "github.com/luthermonson/go-proxmox"

	"github.com/unict-cclab/proxmox-k3s/internal/config"
	"github.com/unict-cclab/proxmox-k3s/internal/ui"
)

const (
	cloudInitRetries  = 3
	cloneTaskTimeout  = 600 // seconds
	configTaskTimeout = 30  // seconds
	startTaskTimeout  = 120 // seconds
	stopTaskTimeout   = 60  // seconds
	deleteTaskTimeout = 60  // seconds
)

type VMSpec struct {
	VMID        int
	Name        string
	ProxmoxNode string
	Cores       int
	Memory      int
	DiskSize    int
	Storage     string
	Bridge      string
	User        string
	SSHPubKey   string
	IPAddress   string
	Gateway     string
	DNS         string
	SubnetMask  int
	ClusterName string
	Role        string
	Labels      []string
	Taints      []string
}

type VMInfo struct {
	VMID int
	Name string
	IP   string
	VM   *pxapi.VirtualMachine
}

func CreateVM(ctx context.Context, c *Client, cfg *config.Config, spec VMSpec, out io.Writer) (*VMInfo, error) {
	existing, err := c.FindVMByName(ctx, spec.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		ui.Info(out, "VM %q already exists (VMID %d), skipping", spec.Name, existing.VMID)
		ip := spec.IPAddress
		if ip == "" {
			ip, _ = WaitForIP(ctx, existing, 2*time.Minute)
		}
		return &VMInfo{VMID: int(existing.VMID), Name: spec.Name, IP: ip, VM: existing}, nil
	}

	tmplName := TemplateName(cfg)
	tmpl, err := c.FindVMByName(ctx, tmplName)
	if err != nil || tmpl == nil {
		return nil, fmt.Errorf("template %q not found; run template creation first", tmplName)
	}

	if spec.VMID == 0 {
		return nil, fmt.Errorf("allocating VMID for %s: missing preassigned VMID", spec.Name)
	}

	nextVMIDStart := spec.VMID
	for attempt := 0; attempt < cloudInitRetries; attempt++ {
		vmid := nextVMIDStart

		ui.Step(out, "cloning %s (VMID %d) from template...", spec.Name, vmid)
		_, cloneTask, err := tmpl.Clone(ctx, &pxapi.VirtualMachineCloneOptions{
			NewID:  vmid,
			Name:   spec.Name,
			Full:   1, // full clone
			Target: spec.ProxmoxNode,
		})
		if err != nil {
			return nil, fmt.Errorf("cloning template for %s: %w", spec.Name, err)
		}
		if err := waitForTaskOK(ctx, cloneTask, cloneTaskTimeout); err != nil {
			return nil, fmt.Errorf("waiting for clone of %s: %w", spec.Name, err)
		}

		node, err := c.Node(ctx, spec.ProxmoxNode)
		if err != nil {
			return nil, err
		}
		vm, err := node.VirtualMachine(ctx, vmid)
		if err != nil {
			return nil, fmt.Errorf("fetching VM %d: %w", vmid, err)
		}

		ui.Step(out, "configuring %s...", spec.Name)
		tag := "proxmox-k3s;" + spec.ClusterName + ";role-" + spec.Role

		body := encodeConfigBody(map[string]string{
			"cores":      fmt.Sprintf("%d", spec.Cores),
			"memory":     fmt.Sprintf("%d", spec.Memory),
			"net0":       fmt.Sprintf("virtio,bridge=%s", spec.Bridge),
			"ciuser":     spec.User,
			"sshkeys":    encodeSSHKey(spec.SSHPubKey),
			"ipconfig0":  buildIPConfig(spec),
			"nameserver": spec.DNS,
			"tags":       tag,
			"onboot":     "1",
			"ciupgrade":  "0",
		})

		configTask, err := c.ConfigVM(ctx, spec.ProxmoxNode, vmid, body)
		if err != nil {
			return nil, fmt.Errorf("configuring %s: %w", spec.Name, err)
		}
		if err := waitForTaskOK(ctx, configTask, configTaskTimeout); err != nil {
			if isCloudInitVolumeConflict(err) && attempt < cloudInitRetries-1 {
				ui.Warn(out, "cloud-init volume for VMID %d already exists, retrying with a new VMID...", vmid)
				if cleanupErr := cleanupFailedVM(ctx, vm, out); cleanupErr != nil {
					ui.Warn(out, "cleanup of failed VM %d: %v", vmid, cleanupErr)
				}
				nextVMIDStart = vmid + 1
				continue
			}
			return nil, fmt.Errorf("waiting for config of %s: %w", spec.Name, err)
		}

		if spec.DiskSize > 0 {
			ui.Step(out, "resizing disk for %s to %dG...", spec.Name, spec.DiskSize)
			if err := vm.ResizeDisk(ctx, "scsi0", fmt.Sprintf("%dG", spec.DiskSize)); err != nil {
				return nil, fmt.Errorf("resizing disk for %s: %w", spec.Name, err)
			}
		}

		ui.Step(out, "starting %s...", spec.Name)
		startTask, err := vm.Start(ctx)
		if err != nil {
			return nil, fmt.Errorf("starting %s: %w", spec.Name, err)
		}
		if err := waitForTaskOK(ctx, startTask, startTaskTimeout); err != nil {
			return nil, fmt.Errorf("waiting for %s to start: %w", spec.Name, err)
		}

		var ip string
		if spec.IPAddress != "" {
			ip = spec.IPAddress
			ui.Info(out, "using configured IP for %s: %s", spec.Name, ip)
		} else {
			ui.Step(out, "waiting for IP on %s...", spec.Name)
			ip, err = WaitForIP(ctx, vm, 3*time.Minute)
			if err != nil {
				return nil, fmt.Errorf("getting IP for %s: %w", spec.Name, err)
			}
		}
		ui.Success(out, "%s is up at %s", spec.Name, ip)

		return &VMInfo{VMID: vmid, Name: spec.Name, IP: ip, VM: vm}, nil
	}

	return nil, fmt.Errorf("creating %s: exhausted VMID retries due to cloud-init volume conflicts", spec.Name)
}

func WaitForIP(ctx context.Context, vm *pxapi.VirtualMachine, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ifaces, err := vm.AgentGetNetworkIFaces(ctx)
		if err == nil {
			for _, iface := range ifaces {
				if iface.Name == "lo" {
					continue
				}
				for _, addr := range iface.IPAddresses {
					if addr.IPAddressType == "ipv4" && !isLoopback(addr.IPAddress) {
						return addr.IPAddress, nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return "", fmt.Errorf("timeout waiting for IP (guest agent must be running)")
}

func DeleteVM(ctx context.Context, c *Client, name string, out io.Writer) error {
	vm, err := c.FindVMByName(ctx, name)
	if err != nil {
		return err
	}
	if vm == nil {
		ui.Info(out, "VM %q not found, skipping", name)
		return nil
	}

	ui.Step(out, "stopping %s (VMID %d)...", name, vm.VMID)
	if vm.Status == "running" {
		stopTask, err := vm.Stop(ctx)
		if err != nil {
			return fmt.Errorf("stopping %s: %w", name, err)
		}
		if err := waitForTaskOK(ctx, stopTask, stopTaskTimeout); err != nil {
			return fmt.Errorf("waiting for %s to stop: %w", name, err)
		}
	}

	ui.Step(out, "deleting %s...", name)
	task, err := vm.Delete(ctx)
	if err != nil {
		return fmt.Errorf("deleting %s: %w", name, err)
	}
	return waitForTaskOK(ctx, task, deleteTaskTimeout)
}

func buildIPConfig(spec VMSpec) string {
	if spec.IPAddress == "" {
		return "ip=dhcp"
	}
	mask := spec.SubnetMask
	if mask == 0 {
		mask = 24
	}
	return fmt.Sprintf("ip=%s/%d,gw=%s", spec.IPAddress, mask, spec.Gateway)
}

func isLoopback(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

func encodeSSHKey(key string) string {
	normalized := strings.TrimSpace(strings.ReplaceAll(key, "\r", ""))
	// Proxmox expects an URL-encoded key string and is picky about spaces being %20, not +.
	return strings.ReplaceAll(url.QueryEscape(normalized), "+", "%20")
}

func encodeConfigBody(values map[string]string) string {
	v := make(url.Values, len(values))
	for key, value := range values {
		v[key] = []string{value}
	}
	return v.Encode()
}

func isCloudInitVolumeConflict(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "cloudinit") && strings.Contains(msg, "File exists")
}

func cleanupFailedVM(ctx context.Context, vm *pxapi.VirtualMachine, out io.Writer) error {
	if vm == nil {
		return nil
	}
	ui.Warn(out, "cleaning up failed VM %s (VMID %d)...", vm.Name, vm.VMID)
	task, err := vm.Delete(ctx)
	if err != nil {
		return err
	}
	return waitForTaskOK(ctx, task, deleteTaskTimeout)
}
