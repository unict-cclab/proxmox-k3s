package proxmox

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	pxapi "github.com/luthermonson/go-proxmox"

	"github.com/amarchese96/proxmox-k3s/internal/config"
	"github.com/amarchese96/proxmox-k3s/internal/ui"
)

func TemplateName(cfg *config.Config) string {
	return cfg.Template.Name
}

func EnsureTemplate(ctx context.Context, c *Client, cfg *config.Config, out io.Writer) error {
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

	ui.Step(out, "[template] downloading cloud image to staging storage %s...", cfg.Template.ImageStorage)
	downloadTask, err := downloadImageToStorage(ctx, c, node, cfg.Template.ImageStorage, imageFile, cfg.Template.CloudImageURL)
	if err != nil {
		return fmt.Errorf("downloading cloud image: %w", err)
	}
	if err := waitForTaskOK(ctx, downloadTask, 1800); err != nil {
		return fmt.Errorf("downloading cloud image: %w", err)
	}

	sourceVolID, err := findDownloadedImageVolID(ctx, node, cfg.Template.ImageStorage, imageFile)
	if err != nil {
		return fmt.Errorf("finding downloaded cloud image in staging storage %q: %w", cfg.Template.ImageStorage, err)
	}
	ui.Info(out, "[template] using source image %s", sourceVolID)

	ui.Step(out, "[template] creating VM shell...")
	createTask, err := node.NewVirtualMachine(ctx, vmid,
		pxapi.VirtualMachineOption{Name: "name", Value: name},
		pxapi.VirtualMachineOption{Name: "memory", Value: 2048},
		pxapi.VirtualMachineOption{Name: "cores", Value: 2},
		pxapi.VirtualMachineOption{Name: "net0", Value: fmt.Sprintf("virtio,bridge=%s", cfg.NodeDefaults.Bridge)},
		pxapi.VirtualMachineOption{Name: "agent", Value: 1},
		pxapi.VirtualMachineOption{Name: "ostype", Value: "l26"},
		pxapi.VirtualMachineOption{Name: "cpu", Value: "host"},
		pxapi.VirtualMachineOption{Name: "serial0", Value: "socket"},
		pxapi.VirtualMachineOption{Name: "vga", Value: "serial0"},
		pxapi.VirtualMachineOption{Name: "scsihw", Value: "virtio-scsi-pci"},
		pxapi.VirtualMachineOption{Name: "scsi0", Value: fmt.Sprintf("%s:0,import-from=%s", cfg.Template.Storage, sourceVolID)},
		pxapi.VirtualMachineOption{Name: "ide2", Value: fmt.Sprintf("%s:cloudinit", cfg.Template.CloudInitStorage)},
		pxapi.VirtualMachineOption{Name: "boot", Value: "c"},
		pxapi.VirtualMachineOption{Name: "bootdisk", Value: "scsi0"},
	)
	if err != nil {
		return fmt.Errorf("creating template VM: %w", err)
	}
	if err := waitForTaskOK(ctx, createTask, 1800); err != nil {
		return fmt.Errorf("creating template VM: %w", err)
	}

	vm, err := node.VirtualMachine(ctx, vmid)
	if err != nil {
		return fmt.Errorf("fetching template VM %d: %w", vmid, err)
	}

	ui.Step(out, "[template] converting to template...")
	templateTask, err := vm.ConvertToTemplate(ctx)
	if err != nil {
		return fmt.Errorf("converting template VM: %w", err)
	}
	if err := waitForTaskOK(ctx, templateTask, 300); err != nil {
		return fmt.Errorf("converting template VM: %w", err)
	}

	ui.Success(out, "template %q created (VMID %d)", name, vmid)
	return nil
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

	cloudInitStorage, err := node.Storage(ctx, cfg.Template.CloudInitStorage)
	if err != nil {
		return fmt.Errorf("checking template.cloud_init_storage %q: %w", cfg.Template.CloudInitStorage, err)
	}
	if !storageHasContent(cloudInitStorage.Content, "images") {
		return fmt.Errorf("template.cloud_init_storage %q on node %s must support content type %q", cfg.Template.CloudInitStorage, node.Name, "images")
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
