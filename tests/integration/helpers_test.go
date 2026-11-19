//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// nodeClock reads the device node's clock; the test host clock may differ from the Docker VM.
func nodeClock(t *testing.T) int64 {
	t.Helper()
	out, err := onNode("date", "+%s")
	if err != nil {
		t.Fatal(err)
	}
	v, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// openCheck opens path read-only, where the device cgroup check happens.
// The subshell matters: a failed redirection on a special builtin exits a POSIX shell.
func openCheck(path string) string {
	return fmt.Sprintf(`if ( : <%[1]s ) 2>/dev/null; then echo SPO_OPEN_OK:%[1]s; else echo SPO_OPEN_DENIED:%[1]s; fi`, path)
}

// probe opens every configured path of d, then idles.
func probe(d *device) string {
	var parts []string
	for _, p := range d.paths() {
		parts = append(parts, openCheck(p))
	}
	return strings.Join(append(parts, "sleep 3600"), "; ")
}

var seq int

func createService(t *testing.T, args ...string) string {
	t.Helper()
	seq++
	name := fmt.Sprintf("spo-t%d-%d", seq, time.Now().UnixNano()%100000)
	full := append(append([]string{"service", "create", "-d", "--name", name}, pin()...), args...)
	if out, err := mgr(full...); err != nil {
		t.Fatalf("create service: %v\n%s", err, out)
	}
	t.Cleanup(func() { removeService(t, name) })
	return name
}

// removeService waits until the device node has no containers left, so devices are free for the next test.
func removeService(t *testing.T, name string) {
	t.Helper()
	_, _ = mgr("service", "rm", name)
	err := waitUntil(60*time.Second, func() bool {
		out, err := wkr("ps", "-aq", "--filter", "label=com.docker.swarm.service.name="+name)
		return err == nil && strings.TrimSpace(out) == ""
	})
	if err != nil {
		t.Logf("service %s containers not removed: %v", name, err)
	}
}

type taskRow struct{ ID, Desired, Current, Error string }

func tasks(t *testing.T, svc string) []taskRow {
	t.Helper()
	out, err := mgr("service", "ps", "--no-trunc", "--format", "{{.ID}}|{{.DesiredState}}|{{.CurrentState}}|{{.Error}}", svc)
	if err != nil {
		t.Fatalf("service ps: %v", err)
	}
	var rows []taskRow
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if p := strings.SplitN(l, "|", 4); len(p) == 4 {
			rows = append(rows, taskRow{p[0], p[1], p[2], p[3]})
		}
	}
	return rows
}

func waitTask(t *testing.T, svc string, d time.Duration, cond func(taskRow) bool) taskRow {
	t.Helper()
	var hit taskRow
	err := waitUntil(d, func() bool {
		for _, r := range tasks(t, svc) {
			if cond(r) {
				hit = r
				return true
			}
		}
		return false
	})
	if err != nil {
		t.Fatalf("service %s: %v; tasks: %+v", svc, err, tasks(t, svc))
	}
	return hit
}

func running(r taskRow) bool {
	return r.Desired == "Running" && strings.HasPrefix(r.Current, "Running")
}

func logs(t *testing.T, svc string) string {
	t.Helper()
	out, _ := mgr("service", "logs", "--raw", "--no-task-ids", svc)
	return out
}

func waitLog(t *testing.T, svc, marker string, count int) {
	t.Helper()
	waitLogFor(t, svc, marker, count, 45*time.Second)
}

func waitLogFor(t *testing.T, svc, marker string, count int, d time.Duration) {
	t.Helper()
	if err := waitUntil(d, func() bool { return strings.Count(logs(t, svc), marker) >= count }); err != nil {
		t.Fatalf("service %s: want %d x %q in logs, got:\n%s", svc, count, marker, logs(t, svc))
	}
}

func containerOf(t *testing.T, taskID string) string {
	t.Helper()
	var cid string
	_ = waitUntil(20*time.Second, func() bool {
		out, err := mgr("inspect", "-f", "{{.Status.ContainerStatus.ContainerID}}", taskID)
		cid = strings.TrimSpace(out)
		return err == nil && len(cid) == 64
	})
	if len(cid) != 64 {
		t.Fatalf("no container for task %s", taskID)
	}
	return cid
}

// assignedPaths returns host paths of the IDs Swarm bound to the task for d's kind.
func assignedPaths(t *testing.T, taskID string, d *device) []string {
	t.Helper()
	out, err := mgr("inspect", "-f", "{{json .GenericResources}}", taskID)
	if err != nil {
		t.Fatal(err)
	}
	var res []struct {
		NamedResourceSpec *struct{ Kind, Value string }
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse generic resources %q: %v", out, err)
	}
	var paths []string
	for _, r := range res {
		if r.NamedResourceSpec != nil && r.NamedResourceSpec.Kind == d.Kind {
			p, ok := d.IDs[r.NamedResourceSpec.Value]
			if !ok {
				t.Fatalf("swarm assigned %s=%s, which the config does not list", d.Kind, r.NamedResourceSpec.Value)
			}
			paths = append(paths, p)
		}
	}
	return paths
}

// Bundle locations: dockerd-managed containerd (dind) and system containerd (most distro installs).
var bundleRoots = []string{
	"/var/run/docker/containerd/daemon/io.containerd.runtime.v2.task/moby/",
	"/run/containerd/io.containerd.runtime.v2.task/moby/",
}

func bundleDevices(t *testing.T, cid string) []string {
	t.Helper()
	var out string
	var err error
	for _, root := range bundleRoots {
		if out, err = onNode("cat", root+cid+"/config.json"); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("read bundle for %s: %v", cid, err)
	}
	var spec struct {
		Linux struct {
			Devices []struct{ Path string } `json:"devices"`
		} `json:"linux"`
	}
	if err := json.Unmarshal([]byte(out), &spec); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, d := range spec.Linux.Devices {
		paths = append(paths, d.Path)
	}
	return paths
}

// configuredIn returns which configured device paths (any kind) appear in got.
func configuredIn(got []string) []string {
	var out []string
	for _, d := range devices {
		for _, p := range d.paths() {
			for _, g := range got {
				if g == p {
					out = append(out, p)
				}
			}
		}
	}
	return out
}

// requireOnlyAssigned checks the workload could open exactly the assigned paths of d.
func requireOnlyAssigned(t *testing.T, svc string, d *device, assigned []string) {
	t.Helper()
	for _, p := range assigned {
		waitLog(t, svc, "SPO_OPEN_OK:"+p, 1)
	}
	l := logs(t, svc)
	for _, p := range d.paths() {
		if !contains(assigned, p) && strings.Contains(l, "SPO_OPEN_OK:"+p) {
			t.Fatalf("workload opened unassigned device %s:\n%s", p, l)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func reserve(d *device, n int) []string {
	return []string{"--generic-resource", fmt.Sprintf("%s=%d", d.Kind, n)}
}
