package proxmox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	pxapi "github.com/luthermonson/go-proxmox"
)

func waitForTaskOK(ctx context.Context, task *pxapi.Task, seconds int) error {
	if task == nil {
		return fmt.Errorf("missing Proxmox task")
	}

	if err := task.WaitFor(ctx, seconds); err != nil {
		return err
	}
	if err := task.Ping(ctx); err != nil {
		return err
	}
	if task.ExitStatus == "" || task.ExitStatus == "OK" {
		return nil
	}

	msg := task.ExitStatus
	if log, err := task.Log(ctx, 0, 50); err == nil && len(log) > 0 {
		msg = taskLogSummary(log)
	}
	return errors.New(msg)
}

func taskLogSummary(log pxapi.Log) string {
	keys := make([]int, 0, len(log))
	for k := range log {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		line := strings.TrimSpace(log[k])
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return "task failed"
	}
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return strings.Join(lines, "; ")
}
