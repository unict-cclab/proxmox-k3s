package proxmox

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	pxapi "github.com/luthermonson/go-proxmox"

	"github.com/amarchese96/proxmox-k3s/internal/config"
	"github.com/amarchese96/proxmox-k3s/internal/ui"
	"github.com/amarchese96/proxmox-k3s/internal/util"
)

const templateSSHUser = "ubuntu"

func TemplateName(cfg *config.Config) string {
	return cfg.Template.Name
}

func EnsureTemplate(ctx context.Context, c *Client, cfg *config.Config, sshKeyPath, sshPubKey string, out io.Writer) error {
	name := TemplateName(cfg)

	existing, err := c.FindVMByName(ctx, name)
	if err != nil {
		return err
	}
	if existing != nil {
		ui.Info(out, "template %q already exists (VMID %d), skipping", name, existing.VMID)
		return nil
	}

	ui.Step(out, "creating template %q on node %s", name, cfg.Template.ProxmoxNode)

	vmid, err := c.NextVMID(ctx, cfg.Template.VMIDBase)
	if err != nil {
		return fmt.Errorf("allocating template VMID: %w", err)
	}
	ui.Info(out, "allocated VMID %d", vmid)

	node, err := c.Node(ctx, cfg.Template.ProxmoxNode)
	if err != nil {
		return err
	}
	if err := validateTemplateStorages(ctx, node, cfg); err != nil {
		return err
	}

	imageFile := filepath.Base(cfg.Template.CloudImageURL)
	imageFile = templateImageFilename(imageFile)

	sourceVolID, err := ensureDownloadedImage(ctx, c, node, cfg, imageFile, out)
	if err != nil {
		return err
	}
	ui.Info(out, "[template] using source image %s", sourceVolID)

	ui.Step(out, "[template] creating VM shell...")
	createTask, err := node.NewVirtualMachine(ctx, vmid,
		pxapi.VirtualMachineOption{Name: "name", Value: name},
		pxapi.VirtualMachineOption{Name: "memory", Value: 2048},
		pxapi.VirtualMachineOption{Name: "cores", Value: 2},
		pxapi.VirtualMachineOption{Name: "net0", Value: fmt.Sprintf("virtio,bridge=%s", cfg.Template.Bridge)},
		pxapi.VirtualMachineOption{Name: "agent", Value: 1},
		pxapi.VirtualMachineOption{Name: "ostype", Value: "l26"},
		pxapi.VirtualMachineOption{Name: "cpu", Value: "host"},
		pxapi.VirtualMachineOption{Name: "serial0", Value: "socket"},
		pxapi.VirtualMachineOption{Name: "vga", Value: "serial0"},
		pxapi.VirtualMachineOption{Name: "scsihw", Value: "virtio-scsi-pci"},
		pxapi.VirtualMachineOption{Name: "scsi0", Value: fmt.Sprintf("%s:0,import-from=%s", cfg.Template.Storage, sourceVolID)},
		pxapi.VirtualMachineOption{Name: "ide2", Value: fmt.Sprintf("%s:cloudinit", cfg.Template.Storage)},
		pxapi.VirtualMachineOption{Name: "boot", Value: "c"},
		pxapi.VirtualMachineOption{Name: "bootdisk", Value: "scsi0"},
	)
	if err != nil {
		return fmt.Errorf("creating template VM: %w", err)
	}
	if err := waitForTaskOK(ctx, createTask, cfg.Template.TimeoutSeconds); err != nil {
		return fmt.Errorf("creating template VM: %w", err)
	}

	ui.Step(out, "[template] configuring first-boot cloud-init...")
	ciConfig := map[string]string{
		"ciuser":     templateSSHUser,
		"sshkeys":    encodeSSHKey(sshPubKey),
		"ipconfig0":  buildTemplateIPConfig(cfg.Template),
		"nameserver": cfg.Template.DNS,
	}
	if cfg.Template.Password != "" {
		ciConfig["cipassword"] = cfg.Template.Password
	}
	configTask, err := c.ConfigVM(ctx, cfg.Template.ProxmoxNode, vmid, encodeConfigBody(ciConfig))
	if err != nil {
		return fmt.Errorf("configuring template VM: %w", err)
	}
	if err := waitForTaskOK(ctx, configTask, cfg.Template.TimeoutSeconds); err != nil {
		return fmt.Errorf("configuring template VM: %w", err)
	}

	vm, err := node.VirtualMachine(ctx, vmid)
	if err != nil {
		return fmt.Errorf("fetching template VM %d: %w", vmid, err)
	}

	if cfg.Template.DiskSize > 0 {
		ui.Step(out, "[template] resizing disk to %dG...", cfg.Template.DiskSize)
		if err := vm.ResizeDisk(ctx, "scsi0", fmt.Sprintf("%dG", cfg.Template.DiskSize)); err != nil {
			return fmt.Errorf("resizing template VM disk: %w", err)
		}
	}

	if err := prepareTemplateGuest(ctx, vm, cfg.Template, sshKeyPath, out); err != nil {
		return err
	}

	ui.Step(out, "[template] converting to template...")
	templateTask, err := vm.ConvertToTemplate(ctx)
	if err != nil {
		return fmt.Errorf("converting template VM: %w", err)
	}
	if err := waitForTaskOK(ctx, templateTask, cfg.Template.TimeoutSeconds); err != nil {
		return fmt.Errorf("converting template VM: %w", err)
	}

	ui.Success(out, "template %q created (VMID %d)", name, vmid)
	return nil
}

func prepareTemplateGuest(ctx context.Context, vm *pxapi.VirtualMachine, tmpl config.TemplateConfig, sshKeyPath string, out io.Writer) error {
	ui.Step(out, "[template] booting guest for package refresh...")
	startTask, err := vm.Start(ctx)
	if err != nil {
		return fmt.Errorf("starting template VM: %w", err)
	}
	if err := waitForTaskOK(ctx, startTask, tmpl.TimeoutSeconds); err != nil {
		return fmt.Errorf("starting template VM: %w", err)
	}

	ip := tmpl.IP
	if ip == "" {
		ui.Step(out, "[template] waiting for DHCP lease...")
		var err error
		ip, err = WaitForIP(ctx, vm, time.Duration(tmpl.TimeoutSeconds)*time.Second)
		if err != nil {
			return fmt.Errorf("getting template IP: %w", err)
		}
	} else {
		ui.Step(out, "[template] using configured static IP...")
	}
	ui.Info(out, "[template] guest reachable at %s", ip)

	ui.Step(out, "[template] waiting for SSH...")
	runner, err := util.WaitForSSH(ip, 22, templateSSHUser, sshKeyPath, time.Duration(tmpl.TimeoutSeconds)*time.Second)
	if err != nil {
		return fmt.Errorf("SSH to template VM: %w", err)
	}
	defer runner.Close()

	ui.Step(out, "[template] waiting for cloud-init to finish...")
	if err := waitForTemplateCloudInit(runner, out); err != nil {
		return fmt.Errorf("waiting for cloud-init in template VM: %w", err)
	}

	ui.Step(out, "[template] upgrading packages...")
	if err := runner.Run("sudo env DEBIAN_FRONTEND=noninteractive apt-get update && sudo env DEBIAN_FRONTEND=noninteractive apt-get -y dist-upgrade && sudo env DEBIAN_FRONTEND=noninteractive apt-get -y autoremove && sudo apt-get clean", out); err != nil {
		return fmt.Errorf("updating packages in template VM: %w", err)
	}

	ui.Step(out, "[template] cleaning cloud-init state...")
	if err := runner.Run("sudo cloud-init clean --logs --machine-id", out); err != nil {
		return fmt.Errorf("cleaning template VM: %w", err)
	}

	ui.Step(out, "[template] shutting down guest...")
	shutdownTask, err := vm.Shutdown(ctx)
	if err != nil {
		return fmt.Errorf("shutting down template VM: %w", err)
	}
	if err := waitForTaskOK(ctx, shutdownTask, tmpl.TimeoutSeconds); err != nil {
		return fmt.Errorf("shutting down template VM: %w", err)
	}

	return nil
}

func waitForTemplateCloudInit(runner *util.Runner, out io.Writer) error {
	status, err := runner.Output(`sh -lc 'sudo cloud-init status --wait; rc=$?; echo "__CI_RC=$rc"; if [ "$rc" -ne 0 ] && [ "$rc" -ne 2 ]; then exit "$rc"; fi'`)
	if strings.TrimSpace(status) != "" {
		fmt.Fprintln(out, status)
	}
	if err != nil {
		return err
	}
	if strings.Contains(status, "__CI_RC=2") {
		ui.Warn(out, "[template] cloud-init finished with recoverable warnings; continuing")
	}
	return nil
}

func buildTemplateIPConfig(tmpl config.TemplateConfig) string {
	if tmpl.IP == "" {
		return "ip=dhcp"
	}
	mask := tmpl.SubnetMask
	if mask == 0 {
		mask = 24
	}
	return fmt.Sprintf("ip=%s/%d,gw=%s", tmpl.IP, mask, tmpl.Gateway)
}

func ensureDownloadedImage(ctx context.Context, c *Client, node *pxapi.Node, cfg *config.Config, imageFile string, out io.Writer) (string, error) {
	sourceVolID, err := findDownloadedImageVolID(ctx, node, cfg.Template.ImageStorage, imageFile)
	if err == nil {
		ui.Info(out, "[template] reusing existing cloud image in staging storage %s", cfg.Template.ImageStorage)
		return sourceVolID, nil
	}

	ui.Step(out, "[template] downloading cloud image to staging storage %s...", cfg.Template.ImageStorage)
	downloadTask, err := downloadImageToStorage(ctx, c, node, cfg.Template.ImageStorage, imageFile, cfg.Template.CloudImageURL)
	if err != nil {
		return "", fmt.Errorf("downloading cloud image: %w", err)
	}
	if err := waitForTaskOK(ctx, downloadTask, cfg.Template.TimeoutSeconds); err != nil {
		return "", fmt.Errorf("downloading cloud image: %w", err)
	}

	sourceVolID, err = findDownloadedImageVolID(ctx, node, cfg.Template.ImageStorage, imageFile)
	if err != nil {
		return "", fmt.Errorf("finding downloaded cloud image in staging storage %q: %w", cfg.Template.ImageStorage, err)
	}
	return sourceVolID, nil
}

func findDownloadedImageVolID(ctx context.Context, node *pxapi.Node, storageName, imageFile string) (string, error) {
	storage, err := node.Storage(ctx, storageName)
	if err != nil {
		return "", err
	}

	content, err := storage.GetContent(ctx)
	if err != nil {
		return "", err
	}

	for _, item := range content {
		if strings.Contains(item.Volid, imageFile) {
			return item.Volid, nil
		}
	}
	return "", fmt.Errorf("image %q not found in storage contents; ensure storage %q supports content type 'images'", imageFile, storageName)
}

func downloadImageToStorage(ctx context.Context, c *Client, node *pxapi.Node, storageName, imageFile, imageURL string) (*pxapi.Task, error) {
	ret, err := node.StorageDownloadURL(ctx, &pxapi.StorageDownloadURLOptions{
		Content:  "import",
		Filename: imageFile,
		Storage:  storageName,
		URL:      imageURL,
	})
	if err != nil {
		return nil, err
	}
	return pxapi.NewTask(pxapi.UPID(ret), c.api), nil
}

func validateTemplateStorages(ctx context.Context, node *pxapi.Node, cfg *config.Config) error {
	imageStorage, err := node.Storage(ctx, cfg.Template.ImageStorage)
	if err != nil {
		return fmt.Errorf("checking template.image_storage %q: %w", cfg.Template.ImageStorage, err)
	}
	if !storageHasContent(imageStorage.Content, "import") {
		return fmt.Errorf("template.image_storage %q on node %s must support content type %q", cfg.Template.ImageStorage, node.Name, "import")
	}

	templateStorage, err := node.Storage(ctx, cfg.Template.Storage)
	if err != nil {
		return fmt.Errorf("checking template.storage %q: %w", cfg.Template.Storage, err)
	}
	if !storageHasContent(templateStorage.Content, "images") {
		return fmt.Errorf("template.storage %q on node %s must support content type %q", cfg.Template.Storage, node.Name, "images")
	}

	return nil
}

func storageHasContent(content, target string) bool {
	for _, item := range strings.Split(content, ",") {
		if strings.TrimSpace(item) == target {
			return true
		}
	}
	return false
}

func templateImageFilename(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".qcow2", ".raw", ".vmdk":
		return name
	default:
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if base == "" {
			base = "cloud-image"
		}
		return base + ".qcow2"
	}
}

func DeleteTemplate(ctx context.Context, c *Client, cfg *config.Config, out io.Writer) error {
	name := TemplateName(cfg)
	vm, err := c.FindVMByName(ctx, name)
	if err != nil {
		return err
	}
	if vm == nil {
		ui.Info(out, "template %q not found, skipping", name)
		return nil
	}

	ui.Step(out, "deleting template %q (VMID %d)...", name, vm.VMID)
	task, err := vm.Delete(ctx)
	if err != nil {
		return fmt.Errorf("deleting template: %w", err)
	}
	return waitForTaskOK(ctx, task, 60)
}
