//go:build integration

package integration

import (
	"strings"
	"testing"
	"time"
)

func gpuDevice(t *testing.T) *device {
	t.Helper()
	for _, d := range devices {
		if d.GPU {
			return d
		}
	}
	t.Skip("no device with \"gpu\": true in config")
	return nil
}

// assignedIDs returns the generic resource values Swarm bound to the task for d's kind.
func assignedIDs(t *testing.T, taskID string, d *device) []string {
	t.Helper()
	var ids []string
	for _, p := range assignedPaths(t, taskID, d) {
		for id, path := range d.IDs {
			if path == p {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// HIL: the workload sees exactly the GPU Swarm assigned. IDs must be the GPU UUIDs.
func TestHILGPUSeesOnlyAssignedGPU(t *testing.T) {
	d := gpuDevice(t)
	svc := createService(t, append(reserve(d, 1), "--restart-condition", "none", e.gpuImage,
		"sh", "-c", "nvidia-smi -L; echo SPO_SMI_DONE; sleep 3600")...)
	task := waitTask(t, svc, 120*time.Second, running)
	ids := assignedIDs(t, task.ID, d)
	if len(ids) != 1 {
		t.Fatalf("assigned %v, want one GPU", ids)
	}
	waitLogFor(t, svc, "SPO_SMI_DONE", 1, 90*time.Second)
	l := logs(t, svc)
	var gpus []string
	for _, line := range strings.Split(l, "\n") {
		if strings.HasPrefix(line, "GPU ") {
			gpus = append(gpus, line)
		}
	}
	if len(gpus) != 1 || !strings.Contains(gpus[0], ids[0]) {
		t.Fatalf("nvidia-smi -L = %q, want exactly %s", gpus, ids[0])
	}
}

// HIL: a CUDA kernel runs on the assigned GPU.
func TestHILGPUComputation(t *testing.T) {
	d := gpuDevice(t)
	svc := createService(t, append(reserve(d, 1), "--restart-condition", "none", e.cudaImage)...)
	waitLogFor(t, svc, "Test PASSED", 1, 5*time.Minute)
}
