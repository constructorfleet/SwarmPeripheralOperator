//go:build integration

package integration

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// G1, G3, G5: a genuine Swarm task, placed by Swarm onto the node holding the device, opens only its device.
func TestGateSwarmTaskReceivesAssignedDevice(t *testing.T) {
	for _, d := range devices {
		t.Run(d.Kind, func(t *testing.T) {
			svc := createService(t, append(reserve(d, 1), "--restart-condition", "none", e.probeImage, "sh", "-c", probe(d))...)
			task := waitTask(t, svc, 60*time.Second, running)

			node, _ := mgr("inspect", "-f", "{{.NodeID}}", task.ID)
			if strings.TrimSpace(node) != e.nodeID {
				t.Fatalf("task placed on %s, want device node %s", node, e.nodeID)
			}
			assigned := assignedPaths(t, task.ID, d)
			if len(assigned) != 1 {
				t.Fatalf("swarm assigned %v, want one %s", assigned, d.Kind)
			}
			requireOnlyAssigned(t, svc, d, assigned)

			cid := containerOf(t, task.ID)
			hc, err := wkr("inspect", "-f", "{{.HostConfig.Privileged}}|{{json .HostConfig.CapAdd}}|{{json .HostConfig.Devices}}|{{json .Mounts}}", cid)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(hc); got != "false|null|[]|[]" && got != "false|null|null|[]" {
				t.Fatalf("container must be unprivileged with no manual devices or mounts, got %s", got)
			}
			// The spec runc consumed already holds the device: injection precedes the process.
			if got := configuredIn(bundleDevices(t, cid)); len(got) != 1 || got[0] != assigned[0] {
				t.Fatalf("configured devices in bundle = %v, want [%s]", got, assigned[0])
			}
			audit, _ := onNode("cat", e.auditLog)
			if !strings.Contains(audit, `"task":"`+task.ID+`"`) || !strings.Contains(audit, `"decision":"inject"`) {
				t.Fatalf("audit log missing injection for %s:\n%s", task.ID, audit)
			}
		})
	}
}

// G5: without an allocation, mknod gives no access; the device cgroup still denies open.
func TestGateUnallocatedTaskCannotOpenDevice(t *testing.T) {
	path := devices[0].paths()[0]
	nums, err := onNode("stat", "-c", "%t %T", path)
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(nums)
	major, _ := strconv.ParseInt(f[0], 16, 64)
	minor, _ := strconv.ParseInt(f[1], 16, 64)
	var present []string
	for _, d := range devices {
		for _, p := range d.paths() {
			present = append(present, fmt.Sprintf("test -e %s && echo SPO_PRESENT:%s", p, p))
		}
	}
	cmd := fmt.Sprintf(`%s; mknod /tmp/d c %d %d || echo SPO_MKNOD_DENIED; %s; sleep 3600`,
		strings.Join(present, "; "), major, minor, openCheck("/tmp/d"))
	svc := createService(t, "--constraint", "node.id=="+e.nodeID, "--restart-condition", "none", e.probeImage, "sh", "-c", cmd)
	waitTask(t, svc, 60*time.Second, running)
	waitLog(t, svc, "SPO_OPEN_DENIED:/tmp/d", 1)
	if l := logs(t, svc); strings.Contains(l, "SPO_OPEN_OK") || strings.Contains(l, "SPO_PRESENT") {
		t.Fatalf("unallocated task reached device:\n%s", l)
	}
}

// G2: env is untrusted. A forged resource variable without a Swarm assignment fails closed.
func TestGateForgedResourceEnvFailsClosed(t *testing.T) {
	target := devices[0]
	cases := map[string][]string{
		"no reservation": {"--constraint", "node.id==" + e.nodeID, "--env", "DOCKER_RESOURCE_" + target.Kind + "=" + target.firstID()},
	}
	if len(devices) > 1 {
		cases["other kind reserved"] = append(reserve(devices[1], 1), "--env", "DOCKER_RESOURCE_"+target.Kind+"="+target.firstID())
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			args := append(append([]string{"--restart-condition", "none"}, extra...), e.probeImage, "sh", "-c", probe(target))
			svc := createService(t, args...)
			row := waitTask(t, svc, 60*time.Second, func(r taskRow) bool { return r.Error != "" })
			if !strings.Contains(row.Error, "spo-runtime") || !strings.Contains(row.Error, "no SPO assignment") {
				t.Fatalf("task error = %q, want spo-runtime fail-closed rejection", row.Error)
			}
			if strings.Contains(logs(t, svc), "SPO_OPEN_OK") {
				t.Fatal("forged task opened a device")
			}
		})
	}
}

// G2: a forged duplicate value cannot widen the authoritative assignment.
func TestGateForgedDuplicateValueIgnored(t *testing.T) {
	d := devices[0]
	svc := createService(t, append(reserve(d, 1), "--env", "DOCKER_RESOURCE_"+d.Kind+"=forged-id",
		"--restart-condition", "none", e.probeImage, "sh", "-c", probe(d))...)
	task := waitTask(t, svc, 60*time.Second, running)
	assigned := assignedPaths(t, task.ID, d)
	requireOnlyAssigned(t, svc, d, assigned)
	if got := configuredIn(bundleDevices(t, containerOf(t, task.ID))); len(got) != 1 || got[0] != assigned[0] {
		t.Fatalf("configured devices in bundle = %v, want only %v", got, assigned)
	}
}

// G6: Swarm will not double-book exclusive devices, and reclaims them only after the holder is gone.
func TestGateExclusiveAllocationAndReclaim(t *testing.T) {
	d := devices[0]
	since := nodeClock(t) - 1
	a := createService(t, append(reserve(d, len(d.IDs)), e.probeImage, "sh", "-c", probe(d))...)
	aTask := waitTask(t, a, 60*time.Second, running)
	aCID := containerOf(t, aTask.ID)

	b := createService(t, append(reserve(d, 1), e.probeImage, "sh", "-c", probe(d))...)
	waitTask(t, b, 30*time.Second, func(r taskRow) bool { return strings.Contains(r.Error, "insufficient resources") })
	time.Sleep(5 * time.Second)
	for _, r := range tasks(t, b) {
		if running(r) {
			t.Fatalf("second exclusive service started while all %s devices were held: %+v", d.Kind, r)
		}
	}

	removeService(t, a)
	bTask := waitTask(t, b, 60*time.Second, running)
	requireOnlyAssigned(t, b, d, assignedPaths(t, bTask.ID, d))
	bCID := containerOf(t, bTask.ID)

	out, err := wkr("events", "--since", strconv.FormatInt(since, 10), "--until", strconv.FormatInt(nodeClock(t)+1, 10),
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
	d := devices[0]
	all := len(d.IDs)
	svc := createService(t, append(reserve(d, all), e.probeImage, "sh", "-c", probe(d))...)
	first := waitTask(t, svc, 60*time.Second, running)
	marker := "SPO_OPEN_OK:" + d.paths()[0]
	waitLog(t, svc, marker, 1)

	step := func(name string, want int, args ...string) {
		t.Helper()
		if out, err := mgr(append([]string{"service"}, args...)...); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		first = waitTask(t, svc, 90*time.Second, func(r taskRow) bool { return running(r) && r.ID != first.ID })
		waitLog(t, svc, marker, want)
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
		t.Fatalf("scaled to 2 with devices for one: %d running, want 1", n)
	}
	if out, err := mgr("service", "scale", "-d", svc+"=1"); err != nil {
		t.Fatalf("scale: %v\n%s", err, out)
	}
	res, _ := mgr("service", "inspect", "-f", "{{json .Spec.TaskTemplate.Resources.Reservations.GenericResources}}", svc)
	if !strings.Contains(res, `"Kind":"`+d.Kind+`"`) {
		t.Fatalf("reservation lost after lifecycle ops: %s", res)
	}
}

// G2: no controller means no devices, while non-SPO tasks keep running. Recovery is automatic.
func TestGateControllerUnavailableFailsClosedAndRecovers(t *testing.T) {
	d := devices[0]
	replicas, err := mgr("service", "inspect", "-f", "{{.Spec.Mode.Replicated.Replicas}}", e.controllerSvc)
	if err != nil {
		t.Fatalf("controller service: %v", err)
	}
	restore := e.controllerSvc + "=" + strings.TrimSpace(replicas)
	t.Cleanup(func() { _, _ = mgr("service", "scale", "-d", restore) })
	if out, err := mgr("service", "scale", e.controllerSvc+"=0"); err != nil {
		t.Fatalf("scale controller: %v\n%s", err, out)
	}

	plain := createService(t, "--constraint", "node.id=="+e.nodeID, e.probeImage, "sh", "-c", "echo SPO_PLAIN_OK; sleep 3600")
	spo := createService(t, append(reserve(d, 1), "--restart-delay", "3s", e.probeImage, "sh", "-c", probe(d))...)

	waitTask(t, plain, 60*time.Second, running)
	waitLog(t, plain, "SPO_PLAIN_OK", 1)
	row := waitTask(t, spo, 60*time.Second, func(r taskRow) bool { return strings.Contains(r.Error, "lookup allocation") })
	if !strings.Contains(row.Error, "spo-runtime") {
		t.Fatalf("error = %q", row.Error)
	}
	if strings.Contains(logs(t, spo), "SPO_OPEN_OK") {
		t.Fatal("task ran without controller")
	}

	if out, err := mgr("service", "scale", "-d", restore); err != nil {
		t.Fatalf("restore controller: %v\n%s", err, out)
	}
	task := waitTask(t, spo, 120*time.Second, running)
	requireOnlyAssigned(t, spo, d, assignedPaths(t, task.ID, d))
}
