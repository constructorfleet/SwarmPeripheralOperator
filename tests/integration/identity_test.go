//go:build integration

package integration

import (
	"strings"
	"testing"
	"time"
)

// F4: the Swarm executor overwrites user-supplied reserved labels, so labels identify the task.
func TestGateExecutorOverwritesForgedSwarmLabels(t *testing.T) {
	svc := createService(t, "--constraint", "node.id=="+e.nodeID,
		"--container-label", "com.docker.swarm.task.id=forged",
		"--container-label", "com.docker.swarm.service.id=forged",
		"--container-label", "com.docker.swarm.node.id=forged",
		e.probeImage, "sleep", "3600")
	task := waitTask(t, svc, 60*time.Second, running)
	cid := containerOf(t, task.ID)
	out, err := wkr("inspect", "-f", `{{index .Config.Labels "com.docker.swarm.task.id"}}|{{index .Config.Labels "com.docker.swarm.node.id"}}`, cid)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(out), task.ID+"|"+e.nodeID; got != want {
		t.Fatalf("labels = %q, want %q", got, want)
	}
}

// Compose path: docker stack deploy carries generic resource reservations unchanged.
func TestGateStackDeployWithComposeGenericResources(t *testing.T) {
	d := devices[0]
	stack := "spost" + strings.ReplaceAll(time.Now().Format("150405.000"), ".", "")
	placement := ""
	if e.mode == modeHost {
		placement = "\n      placement:\n        constraints: [\"node.id==" + e.nodeID + "\"]"
	}
	compose := `services:
  probe:
    image: ` + e.probeImage + `
    command: ["sh", "-c", "` + strings.ReplaceAll(probe(d), `"`, `\"`) + `"]
    deploy:` + placement + `
      resources:
        reservations:
          generic_resources:
            - discrete_resource_spec:
                kind: ` + d.Kind + `
                value: 1
`
	if out, err := mgrStdin([]byte(compose), "stack", "deploy", "-c", "-", stack); err != nil {
		t.Fatalf("stack deploy: %v\n%s", err, out)
	}
	svc := stack + "_probe"
	t.Cleanup(func() {
		_, _ = mgr("stack", "rm", stack)
		removeService(t, svc)
	})
	task := waitTask(t, svc, 60*time.Second, running)
	requireOnlyAssigned(t, svc, d, assignedPaths(t, task.ID, d))
}

// Regression (review finding): image LABELs become container labels. A non-Swarm container
// built to impersonate a running task must not receive that task's devices.
func TestGateImageForgedTaskLabelsRejected(t *testing.T) {
	d := devices[0]
	victim := createService(t, append(reserve(d, 1), e.probeImage, "sh", "-c", probe(d))...)
	task := waitTask(t, victim, 60*time.Second, running)
	svcID, _ := mgr("service", "inspect", "-f", "{{.ID}}", victim)
	assigned := assignedPaths(t, task.ID, d)

	img := "spo-evil-" + task.ID[:8]
	changes := []string{
		"LABEL com.docker.swarm.task.id=" + task.ID,
		"LABEL com.docker.swarm.service.id=" + strings.TrimSpace(svcID),
		"LABEL com.docker.swarm.node.id=" + e.nodeID,
		"LABEL com.docker.swarm.task.name=" + victim + ".1." + task.ID,
		"ENV DOCKER_RESOURCE_" + d.Kind + "=" + d.firstID(),
	}
	if _, err := wkr("create", "--name", img+"-src", e.probeImage); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = wkr("rm", "-f", img+"-src", img+"-run"); _, _ = wkr("rmi", "-f", img) })
	args := []string{"commit"}
	for _, c := range changes {
		args = append(args, "--change", c)
	}
	if out, err := wkr(append(args, img+"-src", img)...); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	labels, _ := wkr("image", "inspect", "-f", `{{index .Config.Labels "com.docker.swarm.task.id"}}`, img)
	if strings.TrimSpace(labels) != task.ID {
		t.Fatalf("forged image label not set: %q", labels)
	}

	out, err := wkr("run", "--name", img+"-run", img, "sh", "-c", openCheck(assigned[0]))
	if err == nil || !strings.Contains(out, "spo-runtime") || !strings.Contains(out, "not a Swarm task") {
		t.Fatalf("impersonating container: err=%v out=%s, want spo-runtime rejection", err, out)
	}
	if strings.Contains(out, "SPO_OPEN_OK") {
		t.Fatal("impersonating container opened the device")
	}
}
