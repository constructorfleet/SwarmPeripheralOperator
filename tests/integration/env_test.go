//go:build integration

// Package integration runs the Phase 0 feasibility suite against genuine Swarm tasks.
//
// SPO_IT_MODE=dind (default) builds a two-node Docker-in-Docker Swarm.
// SPO_IT_MODE=host runs against an installed Swarm described by SPO_HIL_CONFIG.
package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/constructorfleet/SwarmPeripheralOperator/internal/pki"
)

// dindDevices are real kernel devices outside Docker's default cgroup allowlist. /dev/full is a synthetic fallback.
func dindDevices() []*device {
	return []*device{
		{Kind: "SPO_RNG", cdiKind: "spo.local/rng", IDs: map[string]string{"rng0": ""}, candidates: []string{"/dev/hwrng", "/dev/full"}},
		{Kind: "SPO_SERIAL", cdiKind: "spo.local/serial", IDs: map[string]string{"serial0": ""}, candidates: []string{"/dev/ttyS1", "/dev/ttyS0"}},
	}
}

type env struct {
	mode string
	// device node identity, shared by both modes
	nodeID        string
	controllerSvc string
	auditLog      string
	probeImage    string
	gpuImage      string
	cudaImage     string
	// host mode
	managerHost string
	// dind mode
	id        string
	image     string
	network   string
	manager   string
	worker    string
	managerIP string
	dir       string
}

var e *env

func TestMain(m *testing.M) {
	os.Exit(runSuite(m))
}

// runSuite keeps os.Exit out of deferred code so panics and teardown are not swallowed.
func runSuite(m *testing.M) int {
	var err error
	switch mode := os.Getenv("SPO_IT_MODE"); mode {
	case "", modeDind:
		err = setup()
		if e != nil && os.Getenv("SPO_IT_KEEP") == "" {
			defer e.teardown()
		}
	case modeHost:
		err = setupHost()
	default:
		err = fmt.Errorf("unknown SPO_IT_MODE %q", mode)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "setup failed:", err)
		return 1
	}
	return m.Run()
}

func setup() error {
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	id := "spo-it-" + hex.EncodeToString(suffix)
	image := os.Getenv("SPO_DIND_IMAGE")
	if image == "" {
		image = "docker:29-dind"
	}
	dir, err := os.MkdirTemp("", id)
	if err != nil {
		return err
	}
	en := &env{mode: modeDind, id: id, image: image, network: id, manager: id + "-mgr", worker: id + "-wkr", dir: dir,
		controllerSvc: "spo-controller", auditLog: "/var/log/spo/runtime.jsonl", probeImage: "busybox:latest"}
	e = en
	devices = dindDevices()
	fmt.Fprintf(os.Stderr, "feasibility env %s using %s\n", id, image)

	steps := []struct {
		name string
		fn   func() error
	}{
		{"build", en.build}, {"start nodes", en.startNodes}, {"form swarm", en.formSwarm},
		{"configure nodes", en.configureNodes}, {"load images", en.loadImages}, {"deploy controller", en.deployController},
	}
	for _, step := range steps {
		fmt.Fprintf(os.Stderr, "setup: %s\n", step.name)
		if err := step.fn(); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}
	logComponents()
	return nil
}

func (en *env) teardown() {
	_, _ = docker(context.Background(), "rm", "-f", "-v", en.manager, en.worker)
	_, _ = docker(context.Background(), "network", "rm", en.network)
	_ = os.RemoveAll(en.dir)
}

func (en *env) build() error {
	arch, err := docker(context.Background(), "version", "-f", "{{.Server.Arch}}")
	if err != nil {
		return err
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		return err
	}
	for _, cmd := range []string{"spo-runtime", "spo-controller"} {
		c := exec.CommandContext(context.Background(), "go", "build", "-trimpath", "-o", filepath.Join(en.dir, "bin", cmd), "./cmd/"+cmd)
		c.Dir = root
		c.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+strings.TrimSpace(arch))
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("build %s: %w: %s", cmd, err, out)
		}
	}
	return nil
}

func (en *env) startNodes() error {
	ctx := context.Background()
	if _, err := docker(ctx, "network", "create", en.network); err != nil {
		return err
	}
	for _, n := range []struct {
		name   string
		worker bool
	}{{en.manager, false}, {en.worker, true}} {
		daemon := map[string]any{
			"runtimes":        map[string]any{"spo": map[string]string{"path": "/opt/spo/bin/spo-runtime"}},
			"default-runtime": "spo",
		}
		if n.worker {
			var res []string
			for _, d := range devices {
				for id := range d.IDs {
					res = append(res, d.Kind+"="+id)
				}
			}
			daemon["node-generic-resources"] = res
		}
		cfg, _ := json.Marshal(daemon)
		// dockerd must see daemon.json at first start.
		etc := filepath.Join(en.dir, n.name+"-etc-docker")
		if err := os.MkdirAll(etc, 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(etc, "daemon.json"), cfg, 0o600); err != nil {
			return err
		}
		if _, err := docker(ctx, "create", "--privileged", "--name", n.name, "--hostname", n.name, "--network", en.network,
			"-e", "DOCKER_TLS_CERTDIR=", "-v", filepath.Join(en.dir, "bin")+":/opt/spo/bin:ro",
			"-v", etc+":/etc/docker:ro", en.image); err != nil {
			return err
		}
		if _, err := docker(ctx, "start", n.name); err != nil {
			return err
		}
	}
	for _, n := range []string{en.manager, en.worker} {
		if err := waitUntil(60*time.Second, func() bool { _, err := dexec(n, "docker", "info"); return err == nil }); err != nil {
			return fmt.Errorf("dockerd on %s not ready", n)
		}
	}
	ip, err := docker(ctx, "inspect", "-f", "{{(index .NetworkSettings.Networks \""+en.network+"\").IPAddress}}", en.manager)
	en.managerIP = strings.TrimSpace(ip)
	return err
}

func (en *env) formSwarm() error {
	if _, err := dexec(en.manager, "docker", "swarm", "init", "--advertise-addr", en.managerIP); err != nil {
		return err
	}
	token, err := dexec(en.manager, "docker", "swarm", "join-token", "-q", "worker")
	if err != nil {
		return err
	}
	if _, err := dexec(en.worker, "docker", "swarm", "join", "--token", strings.TrimSpace(token), en.managerIP+":2377"); err != nil {
		return err
	}
	id, err := dexec(en.worker, "docker", "info", "-f", "{{.Swarm.NodeID}}")
	en.nodeID = strings.TrimSpace(id)
	return err
}

func (en *env) configureNodes() error {
	ca, err := pki.NewCA(en.id + "-ca")
	if err != nil {
		return err
	}
	srv, err := ca.Issue("spo-controller", []string{"spo-controller"}, []net.IP{net.ParseIP(en.managerIP)}, x509.ExtKeyUsageServerAuth)
	if err != nil {
		return err
	}
	files := map[string][]byte{"ca.crt": ca.CertPEM(), "controller.crt": srv.CertPEM, "controller.key": srv.KeyPEM}
	for name, data := range files {
		if err := copyInto(en.manager, "/root/spo-secrets/"+name, data); err != nil {
			return err
		}
	}

	for _, node := range []string{en.manager, en.worker} {
		nodeID, err := dexec(node, "docker", "info", "-f", "{{.Swarm.NodeID}}")
		if err != nil {
			return err
		}
		leaf, err := ca.Issue(strings.TrimSpace(nodeID), nil, nil, x509.ExtKeyUsageClientAuth)
		if err != nil {
			return err
		}
		kinds := map[string]string{}
		for _, d := range devices {
			kinds[d.Kind] = d.cdiKind
		}
		rt, _ := json.Marshal(map[string]any{
			"runc":     "/usr/local/bin/runc",
			"kinds":    kinds,
			"auditLog": "/var/log/spo/runtime.jsonl",
			"controller": map[string]string{
				"url": "https://" + en.managerIP + ":7443", "serverName": "spo-controller",
				"caFile": "/etc/spo/ca.crt", "certFile": "/etc/spo/node.crt", "keyFile": "/etc/spo/node.key",
			},
			"lookupTimeout": "5s",
		})
		for path, data := range map[string][]byte{
			"/etc/spo/runtime.json": rt, "/etc/spo/ca.crt": ca.CertPEM(), "/etc/spo/node.crt": leaf.CertPEM, "/etc/spo/node.key": leaf.KeyPEM,
		} {
			if err := copyInto(node, path, data); err != nil {
				return err
			}
		}
	}

	for _, d := range devices {
		id := d.firstID()
		for _, c := range d.candidates {
			if _, err := dexec(en.worker, "test", "-c", c); err == nil {
				d.IDs[id] = c
				break
			}
		}
		if d.IDs[id] == "" {
			return fmt.Errorf("no candidate device for %s on worker", d.Kind)
		}
		fmt.Fprintf(os.Stderr, "device %s=%s -> %s\n", d.Kind, id, d.IDs[id])
		spec, _ := json.Marshal(map[string]any{
			"cdiVersion": "0.6.0", "kind": d.cdiKind,
			"devices": []any{map[string]any{"name": id, "containerEdits": map[string]any{"deviceNodes": []any{map[string]string{"path": d.IDs[id]}}}}},
		})
		if err := copyInto(en.worker, "/etc/cdi/spo-"+strings.ToLower(d.Kind)+".json", spec); err != nil {
			return err
		}
	}
	for _, node := range []string{en.manager, en.worker} {
		if _, err := dexec(node, "sh", "-c", "mkdir -p /etc/cdi && chown -R root:root /etc/spo /etc/cdi && chmod -R go-w /etc/spo /etc/cdi && chmod 600 /etc/spo/node.key"); err != nil {
			return err
		}
	}
	return nil
}

func (en *env) loadImages() error {
	if _, err := docker(context.Background(), "image", "inspect", "busybox:latest"); err != nil {
		if _, err := docker(context.Background(), "pull", "-q", "busybox:latest"); err != nil {
			return err
		}
	}
	img, err := exec.CommandContext(context.Background(), "docker", "save", "busybox:latest").Output()
	if err != nil {
		return err
	}
	for _, node := range []string{en.manager, en.worker} {
		c := exec.CommandContext(context.Background(), "docker", "exec", "-i", node, "docker", "load", "-q")
		c.Stdin = bytes.NewReader(img)
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("load image on %s: %w: %s", node, err, out)
		}
	}
	return nil
}

func (en *env) deployController() error {
	for _, s := range [][2]string{{"spo-ca.crt", "ca.crt"}, {"spo-controller.crt", "controller.crt"}, {"spo-controller.key", "controller.key"}} {
		if _, err := mgr("secret", "create", s[0], "/root/spo-secrets/"+s[1]); err != nil {
			return err
		}
	}
	var kinds []string
	for _, d := range devices {
		kinds = append(kinds, d.Kind)
	}
	if _, err := mgr("service", "create", "-d", "--name", "spo-controller", "--constraint", "node.role==manager",
		"--mount", "type=bind,src=/var/run/docker.sock,dst=/var/run/docker.sock",
		"--mount", "type=bind,src=/opt/spo/bin,dst=/opt/spo/bin,readonly",
		"--secret", "spo-ca.crt", "--secret", "spo-controller.crt", "--secret", "spo-controller.key",
		"--publish", "published=7443,target=7443,mode=host",
		"busybox:latest", "/opt/spo/bin/spo-controller", "--kinds", strings.Join(kinds, ",")); err != nil {
		return err
	}
	return waitUntil(60*time.Second, func() bool {
		_, err := dexec(en.worker, "nc", "-z", "-w", "1", en.managerIP, "7443")
		return err == nil
	})
}

// docker runs the host docker CLI.
func docker(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

// dexec runs a command inside a dind node.
func dexec(node string, args ...string) (string, error) {
	return docker(context.Background(), append([]string{"exec", node}, args...)...)
}

func copyInto(node, path string, data []byte) error {
	c := exec.CommandContext(context.Background(), "docker", "exec", "-i", node, "sh", "-c", "mkdir -p \"$(dirname \"$1\")\" && cat > \"$1\"", "sh", path)
	c.Stdin = bytes.NewReader(data)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("copy %s into %s: %w: %s", path, node, err, out)
	}
	return nil
}

func waitUntil(d time.Duration, cond func() bool) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("condition not met within %s", d)
}
