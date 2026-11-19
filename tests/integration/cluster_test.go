//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// Modes. dind builds a throwaway Swarm; host runs against an operator-installed real Swarm.
const (
	modeDind = "dind"
	modeHost = "host"
)

// device is one SPO-managed generic resource kind on the device node.
type device struct {
	Kind string `json:"kind"`
	// GPU enables nvidia-smi and CUDA checks.
	GPU bool `json:"gpu"`
	// IDs maps advertised generic resource values to host device paths.
	IDs map[string]string `json:"ids"`

	cdiKind    string   // dind only
	candidates []string // dind only; first existing path wins
}

func (d *device) paths() []string {
	var out []string
	for _, p := range d.IDs {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

func (d *device) firstID() string {
	ids := make([]string, 0, len(d.IDs))
	for id := range d.IDs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids[0]
}

var devices []*device

// hilConfig is the SPO_HIL_CONFIG file for host mode.
type hilConfig struct {
	ManagerHost       string    `json:"managerHost"`
	ControllerService string    `json:"controllerService"`
	AuditLog          string    `json:"auditLog"`
	ProbeImage        string    `json:"probeImage"`
	GPUImage          string    `json:"gpuImage"`
	CUDASampleImage   string    `json:"cudaSampleImage"`
	Devices           []*device `json:"devices"`
}

func loadHILConfig(path string) (hilConfig, error) {
	cfg := hilConfig{
		ControllerService: "spo-controller",
		AuditLog:          "/var/log/spo/runtime.jsonl",
		ProbeImage:        "busybox:latest",
		GPUImage:          "nvidia/cuda:12.4.1-base-ubuntu22.04",
		CUDASampleImage:   "nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda12.5.0",
	}
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied test config
	if err != nil {
		return cfg, fmt.Errorf("read SPO_HIL_CONFIG: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(cfg.Devices) == 0 {
		return cfg, errors.New("config lists no devices")
	}
	for _, d := range cfg.Devices {
		if d.Kind == "" || len(d.IDs) == 0 {
			return cfg, fmt.Errorf("device %q needs kind and ids", d.Kind)
		}
	}
	return cfg, nil
}

// setupHost checks that an operator-installed Swarm matches the config. It changes nothing.
func setupHost() error {
	cfg, err := loadHILConfig(os.Getenv("SPO_HIL_CONFIG"))
	if err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return errors.New("host mode reads bundle specs and the audit log; run as root (make hil uses sudo)")
	}
	e = &env{mode: modeHost, managerHost: cfg.ManagerHost, controllerSvc: cfg.ControllerService, auditLog: cfg.AuditLog,
		probeImage: cfg.ProbeImage, gpuImage: cfg.GPUImage, cudaImage: cfg.CUDASampleImage}
	devices = cfg.Devices

	info, err := wkr("info", "-f", "{{.Swarm.LocalNodeState}}|{{.Swarm.NodeID}}|{{.DefaultRuntime}}|{{json .Runtimes}}")
	if err != nil {
		return err
	}
	f := strings.SplitN(strings.TrimSpace(info), "|", 4)
	if len(f) != 4 || f[0] != "active" {
		return fmt.Errorf("this node is not in an active Swarm: %s", info)
	}
	e.nodeID = f[1]
	var runtimes map[string]struct{ Path string }
	if err := json.Unmarshal([]byte(f[3]), &runtimes); err != nil || !strings.HasSuffix(runtimes[f[2]].Path, "spo-runtime") {
		return fmt.Errorf("default runtime %q is not spo-runtime (runtimes: %s)", f[2], f[3])
	}

	adv, err := mgr("node", "inspect", e.nodeID, "-f", "{{json .Description.Resources.GenericResources}}")
	if err != nil {
		return err
	}
	for _, d := range devices {
		for id, path := range d.IDs {
			if !strings.Contains(adv, `"Kind":"`+d.Kind+`","Value":"`+id+`"`) {
				return fmt.Errorf("node does not advertise %s=%s (node-generic-resources: %s)", d.Kind, id, adv)
			}
			if _, err := onNode("test", "-c", path); err != nil {
				return fmt.Errorf("%s=%s: %s is not a character device on this node", d.Kind, id, path)
			}
		}
	}
	if out, err := mgr("service", "ps", "-f", "desired-state=running", "--format", "{{.CurrentState}}", e.controllerSvc); err != nil || !strings.Contains(out, "Running") {
		return fmt.Errorf("controller service %q is not running: %s %w", e.controllerSvc, out, err)
	}
	images := []string{e.probeImage}
	for _, d := range devices {
		if d.GPU {
			images = append(images, e.gpuImage, e.cudaImage)
		}
	}
	for _, img := range images {
		if _, err := wkr("image", "inspect", img); err != nil {
			if out, err := wkr("pull", "-q", img); err != nil {
				return fmt.Errorf("pull %s: %w: %s", img, err, out)
			}
		}
	}
	logComponents()
	return nil
}

func logComponents() {
	if v, err := wkr("version", "-f", "{{range .Server.Components}}{{.Name}}={{.Version}} {{end}}"); err == nil {
		fmt.Fprintf(os.Stderr, "engine components: %s\n", strings.TrimSpace(v))
	}
	for _, d := range devices {
		fmt.Fprintf(os.Stderr, "device %s %v gpu=%v\n", d.Kind, d.IDs, d.GPU)
	}
}

// mgr runs the docker CLI against a Swarm manager.
func mgr(args ...string) (string, error) {
	return mgrStdin(nil, args...)
}

func mgrStdin(stdin []byte, args ...string) (string, error) {
	if e.mode == modeHost {
		if e.managerHost != "" {
			args = append([]string{"-H", e.managerHost}, args...)
		}
		return run("docker", stdin, args...)
	}
	flags := []string{"exec"}
	if stdin != nil {
		flags = append(flags, "-i")
	}
	return run("docker", stdin, append(append(flags, e.manager, "docker"), args...)...)
}

// wkr runs the docker CLI against the device node's local daemon.
func wkr(args ...string) (string, error) {
	if e.mode == modeHost {
		return run("docker", nil, args...)
	}
	return dexec(e.worker, append([]string{"docker"}, args...)...)
}

// onNode runs a command in the device node's host namespace.
func onNode(args ...string) (string, error) {
	if e.mode == modeHost {
		return run(args[0], nil, args[1:]...)
	}
	return dexec(e.worker, args...)
}

func run(name string, stdin []byte, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		c.Stdin = bytes.NewReader(stdin)
	}
	out, err := c.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

// pin keeps host-mode services on the device node; other nodes may advertise the same kinds.
func pin() []string {
	if e.mode == modeHost {
		return []string{"--constraint", "node.id==" + e.nodeID}
	}
	return nil
}
