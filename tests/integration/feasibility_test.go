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

// workerClock reads the worker's clock; the test host clock may differ from the Docker VM.
func workerClock(t *testing.T) int64 {
	t.Helper()
	out, err := dexec(e.worker, "date", "+%s")
	if err != nil {
		t.Fatal(err)
	}
	v, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func deviceByKind(kind string) *device {
	for _, d := range devices {
		if d.Kind == kind {
			return d
		}
	}
	panic("unknown kind " + kind)
}

// openCheck opens path read-only, where the device cgroup check happens.
// The subshell matters: a failed redirection on a special builtin exits a POSIX shell.
func openCheck(path string) string {
	return fmt.Sprintf(`if ( : <%s ) 2>/dev/null; then echo SPO_OPEN_OK; else echo SPO_OPEN_DENIED; fi`, path)
}

func probe(path string) string { return openCheck(path) + "; sleep 3600" }

var seq int

func createService(t *testing.T, args ...string) string {
	t.Helper()
	seq++
	name := fmt.Sprintf("t%d-%d", seq, time.Now().UnixNano()%100000)
	full := append([]string{"service", "create", "-d", "--name", name}, args...)
	if out, err := mgr(full...); err != nil {
		t.Fatalf("create service: %v\n%s", err, out)
	}
	t.Cleanup(func() { removeService(t, name) })
	return name
}

// removeService waits until the worker has no containers left, so devices are free for the next test.
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
	if err := waitUntil(45*time.Second, func() bool { return strings.Count(logs(t, svc), marker) >= count }); err != nil {
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

func bundleDevices(t *testing.T, cid string) []string {
	t.Helper()
	out, err := dexec(e.worker, "cat", "/var/run/docker/containerd/daemon/io.containerd.runtime.v2.task/moby/"+cid+"/config.json")
	if err != nil {
		t.Fatalf("read bundle: %v", err)
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

// G1, G3, G5: a genuine Swarm task, placed by Swarm onto the node holding the device, opens it.
func TestGateSwarmTaskReceivesAssignedDevice(t *testing.T) {
	for _, d := range devices {
		t.Run(d.Kind, func(t *testing.T) {
			svc := createService(t, "--generic-resource", d.Kind+"=1", "--restart-condition", "none",
				"busybox:latest", "sh", "-c", probe(d.Path))
			task := waitTask(t, svc, 60*time.Second, running)
			waitLog(t, svc, "SPO_OPEN_OK", 1)

			node, _ := mgr("inspect", "-f", "{{.NodeID}}", task.ID)
			if strings.TrimSpace(node) != e.workerID {
				t.Fatalf("task placed on %s, want worker %s", node, e.workerID)
			}
			assigned, _ := mgr("inspect", "-f", "{{json .GenericResources}}", task.ID)
			if !strings.Contains(assigned, `"Value":"`+d.ID+`"`) {
				t.Fatalf("swarm assignment = %s, want %s", assigned, d.ID)
			}

			cid := containerOf(t, task.ID)
			hc, err := wkr("inspect", "-f", "{{.HostConfig.Privileged}}|{{json .HostConfig.CapAdd}}|{{json .HostConfig.Devices}}|{{json .Mounts}}", cid)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(hc); got != "false|null|[]|[]" && got != "false|null|null|[]" {
				t.Fatalf("container must be unprivileged with no manual devices or mounts, got %s", got)
			}
			// The spec runc consumed already holds the device: injection precedes the process.
			if got := bundleDevices(t, cid); len(got) != 1 || got[0] != d.Path {
				t.Fatalf("bundle devices = %v, want [%s]", got, d.Path)
			}
			audit, _ := dexec(e.worker, "cat", "/var/log/spo/runtime.jsonl")
			if !strings.Contains(audit, `"task":"`+task.ID+`"`) || !strings.Contains(audit, `"decision":"inject"`) {
				t.Fatalf("audit log missing injection for %s:\n%s", task.ID, audit)
			}
		})
	}
}

// G5: without an allocation, mknod gives no access; the device cgroup still denies open.
func TestGateUnallocatedTaskCannotOpenDevice(t *testing.T) {
	d := deviceByKind("SPO_RNG")
	nums, err := dexec(e.worker, "stat", "-c", "%t %T", d.Path)
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(nums)
	major, _ := strconv.ParseInt(f[0], 16, 64)
	minor, _ := strconv.ParseInt(f[1], 16, 64)
	cmd := fmt.Sprintf(`test -e %s && echo SPO_PRESENT; mknod /tmp/d c %d %d || echo SPO_MKNOD_DENIED; %s; sleep 3600`, d.Path, major, minor, openCheck("/tmp/d"))
	svc := createService(t, "--constraint", "node.role==worker", "--restart-condition", "none", "busybox:latest", "sh", "-c", cmd)
	waitTask(t, svc, 60*time.Second, running)
	waitLog(t, svc, "SPO_OPEN_DENIED", 1)
	if l := logs(t, svc); strings.Contains(l, "SPO_OPEN_OK") || strings.Contains(l, "SPO_PRESENT") {
		t.Fatalf("unallocated task reached device:\n%s", l)
	}
}

// G2: env is untrusted. A forged resource variable without a Swarm assignment fails closed.
func TestGateForgedResourceEnvFailsClosed(t *testing.T) {
	rng := deviceByKind("SPO_RNG")
	cases := map[string][]string{
		"no reservation":      {"--constraint", "node.role==worker", "--env", "DOCKER_RESOURCE_SPO_RNG=" + rng.ID},
		"other kind reserved": {"--generic-resource", "SPO_SERIAL=1", "--env", "DOCKER_RESOURCE_SPO_RNG=" + rng.ID},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			args := append(append([]string{"--restart-condition", "none"}, extra...), "busybox:latest", "sh", "-c", probe(rng.Path))
			svc := createService(t, args...)
			row := waitTask(t, svc, 60*time.Second, func(r taskRow) bool { return r.Error != "" })
			if !strings.Contains(row.Error, "spo-runtime") || !strings.Contains(row.Error, "no SPO assignment") {
				t.Fatalf("task error = %q, want spo-runtime fail-closed rejection", row.Error)
			}
			if strings.Contains(logs(t, svc), "SPO_OPEN_OK") {
				t.Fatal("forged task opened the device")
			}
		})
	}
}

// G2: a forged duplicate value cannot widen the authoritative assignment.
func TestGateForgedDuplicateValueIgnored(t *testing.T) {
	rng, serial := deviceByKind("SPO_RNG"), deviceByKind("SPO_SERIAL")
	svc := createService(t, "--generic-resource", "SPO_RNG=1", "--env", "DOCKER_RESOURCE_SPO_RNG="+serial.ID,
		"--restart-condition", "none", "busybox:latest", "sh", "-c", probe(rng.Path))
	task := waitTask(t, svc, 60*time.Second, running)
	waitLog(t, svc, "SPO_OPEN_OK", 1)
	if got := bundleDevices(t, containerOf(t, task.ID)); len(got) != 1 || got[0] != rng.Path {
		t.Fatalf("bundle devices = %v, want only %s", got, rng.Path)
	}
}

// G6: Swarm will not double-book an exclusive device, and reclaims it only after the holder is gone.
func TestGateExclusiveAllocationAndReclaim(t *testing.T) {
	rng := deviceByKind("SPO_RNG")
	since := workerClock(t) - 1
	a := createService(t, "--generic-resource", "SPO_RNG=1", "busybox:latest", "sh", "-c", probe(rng.Path))
	aTask := waitTask(t, a, 60*time.Second, running)
	aCID := containerOf(t, aTask.ID)

	b := createService(t, "--generic-resource", "SPO_RNG=1", "busybox:latest", "sh", "-c", probe(rng.Path))
	waitTask(t, b, 30*time.Second, func(r taskRow) bool { return strings.Contains(r.Error, "insufficient resources") })
	time.Sleep(5 * time.Second)
	for _, r := range tasks(t, b) {
		if running(r) {
			t.Fatalf("second exclusive service started while device held: %+v", r)
		}
	}

	removeService(t, a)
	bTask := waitTask(t, b, 60*time.Second, running)
	waitLog(t, b, "SPO_OPEN_OK", 1)
	bCID := containerOf(t, bTask.ID)

	out, err := wkr("events", "--since", strconv.FormatInt(since, 10), "--until", strconv.FormatInt(workerClock(t)+1, 10),
		"--filter", "type=container", "--format", "{{.TimeNano}} {{.Action}} {{.Actor.ID}}")
	if err != nil {
		t.Fatal(err)
	}
	var aDie, bStart int64
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) != 3 {
			continue
		}
		ts, _ := strconv.ParseInt(f[0], 10, 64)
		switch {
		case f[2] == aCID && f[1] == "die":
			aDie = ts
		case f[2] == bCID && f[1] == "start":
			bStart = ts
		}
	}
	if aDie == 0 || bStart == 0 || aDie >= bStart {
		t.Fatalf("want holder die before successor start; die=%d start=%d\n%s", aDie, bStart, out)
	}
}

// G4: update, rollback, and scale stay native and keep device access.
func TestGateNativeLifecycle(t *testing.T) {
	rng := deviceByKind("SPO_RNG")
	svc := createService(t, "--generic-resource", "SPO_RNG=1", "busybox:latest", "sh", "-c", probe(rng.Path))
	first := waitTask(t, svc, 60*time.Second, running)
	waitLog(t, svc, "SPO_OPEN_OK", 1)

	step := func(name string, want int, args ...string) taskRow {
		t.Helper()
		if out, err := mgr(append([]string{"service"}, args...)...); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		row := waitTask(t, svc, 90*time.Second, func(r taskRow) bool { return running(r) && r.ID != first.ID })
		waitLog(t, svc, "SPO_OPEN_OK", want)
		first = row
		return row
	}
	step("update", 2, "update", "-d", "--env-add", "SPO_GEN=2", svc)
	step("rollback", 3, "rollback", "-d", svc)

	if out, err := mgr("service", "scale", "-d", svc+"=2"); err != nil {
		t.Fatalf("scale: %v\n%s", err, out)
	}
	waitTask(t, svc, 30*time.Second, func(r taskRow) bool { return strings.Contains(r.Error, "insufficient resources") })
	n := 0
	for _, r := range tasks(t, svc) {
		if running(r) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("scaled to 2 with one device: %d running, want 1", n)
	}
	if out, err := mgr("service", "scale", "-d", svc+"=1"); err != nil {
		t.Fatalf("scale: %v\n%s", err, out)
	}

	res, _ := mgr("service", "inspect", "-f", "{{json .Spec.TaskTemplate.Resources.Reservations.GenericResources}}", svc)
	if !strings.Contains(res, `"Kind":"SPO_RNG"`) {
		t.Fatalf("reservation lost after lifecycle ops: %s", res)
	}
}

// G2: no controller means no devices, while non-SPO tasks keep running. Recovery is automatic.
func TestGateControllerUnavailableFailsClosedAndRecovers(t *testing.T) {
	rng := deviceByKind("SPO_RNG")
	t.Cleanup(func() { _, _ = mgr("service", "scale", "-d", "spo-controller=1") })
	if out, err := mgr("service", "scale", "spo-controller=0"); err != nil {
		t.Fatalf("scale controller: %v\n%s", err, out)
	}

	plain := createService(t, "--constraint", "node.role==worker", "busybox:latest", "sh", "-c", "echo SPO_PLAIN_OK; sleep 3600")
	spo := createService(t, "--generic-resource", "SPO_RNG=1", "--restart-delay", "3s", "busybox:latest", "sh", "-c", probe(rng.Path))

	waitTask(t, plain, 60*time.Second, running)
	waitLog(t, plain, "SPO_PLAIN_OK", 1)
	row := waitTask(t, spo, 60*time.Second, func(r taskRow) bool { return strings.Contains(r.Error, "lookup allocation") })
	if !strings.Contains(row.Error, "spo-runtime") {
		t.Fatalf("error = %q", row.Error)
	}
	if strings.Contains(logs(t, spo), "SPO_OPEN_OK") {
		t.Fatal("task ran without controller")
	}

	if out, err := mgr("service", "scale", "-d", "spo-controller=1"); err != nil {
		t.Fatalf("restore controller: %v\n%s", err, out)
	}
	waitTask(t, spo, 120*time.Second, running)
	waitLog(t, spo, "SPO_OPEN_OK", 1)
}
