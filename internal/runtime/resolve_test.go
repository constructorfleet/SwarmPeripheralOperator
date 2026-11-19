package runtime

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/constructorfleet/SwarmPeripheralOperator/pkg/api"
)

const ctrID = "813d42e68dbc6a5dc25de74c49e11bde933e41bc066452f1d0439dabbbe63b20"

type fakeLabeler struct {
	labels map[string]string
	err    error
	calls  int
}

func (f *fakeLabeler) ContainerLabels(context.Context, string) (map[string]string, error) {
	f.calls++
	return f.labels, f.err
}

type fakeSource struct {
	alloc api.Allocation
	err   error
	asked string
}

func (f *fakeSource) Allocation(_ context.Context, taskID string) (api.Allocation, error) {
	f.asked = taskID
	return f.alloc, f.err
}

var kinds = map[string]string{"SPO_RNG": "spo.local/rng", "SPO_SERIAL": "spo.local/serial"}

func swarmLabels() map[string]string {
	return map[string]string{
		"com.docker.swarm.task.id":    "task1",
		"com.docker.swarm.service.id": "svc1",
		"com.docker.swarm.node.id":    "node1",
	}
}

func goodAlloc(a ...api.Assignment) api.Allocation {
	return api.Allocation{TaskID: "task1", ServiceID: "svc1", NodeID: "node1", TaskVersion: 7, Assignments: a}
}

func newResolver(l *fakeLabeler, s *fakeSource) *Resolver {
	return &Resolver{Labels: l, Source: s, Kinds: kinds}
}

func TestPassthroughWithoutSPOHint(t *testing.T) {
	l, s := &fakeLabeler{labels: swarmLabels()}, &fakeSource{}
	plan, err := newResolver(l, s).Resolve(context.Background(), ctrID, []string{"PATH=/bin", "DOCKER_RESOURCE_OTHER=x"})
	if err != nil || !plan.Passthrough {
		t.Fatalf("plan=%+v err=%v, want passthrough", plan, err)
	}
	if l.calls != 0 || s.asked != "" {
		t.Fatal("passthrough must not query daemon or controller")
	}
}

func TestInjectsOnlyAuthoritativeAssignments(t *testing.T) {
	l := &fakeLabeler{labels: swarmLabels()}
	s := &fakeSource{alloc: goodAlloc(api.Assignment{Kind: "SPO_RNG", ID: "hwrng"})}
	// User env forges a second device; Swarm appends the real one.
	env := []string{"DOCKER_RESOURCE_SPO_RNG=other", "DOCKER_RESOURCE_SPO_RNG=hwrng"}
	plan, err := newResolver(l, s).Resolve(context.Background(), ctrID, env)
	if err != nil {
		t.Fatal(err)
	}
	if s.asked != "task1" {
		t.Fatalf("asked controller for %q, want task1", s.asked)
	}
	if want := []string{"spo.local/rng=hwrng"}; !reflect.DeepEqual(plan.Devices, want) {
		t.Fatalf("devices = %v, want %v", plan.Devices, want)
	}
	if plan.Passthrough || plan.Allocation.TaskVersion != 7 {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestRejectsHintWithoutAssignment(t *testing.T) {
	l := &fakeLabeler{labels: swarmLabels()}
	s := &fakeSource{alloc: goodAlloc()}
	_, err := newResolver(l, s).Resolve(context.Background(), ctrID, []string{"DOCKER_RESOURCE_SPO_RNG=hwrng"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestRejectsHintOnNonSwarmContainer(t *testing.T) {
	l := &fakeLabeler{labels: map[string]string{"app": "x"}}
	_, err := newResolver(l, &fakeSource{}).Resolve(context.Background(), ctrID, []string{"DOCKER_RESOURCE_SPO_RNG=hwrng"})
	if !errors.Is(err, ErrNotSwarmTask) {
		t.Fatalf("err = %v, want ErrNotSwarmTask", err)
	}
}

func TestRejectsMismatchedControllerIdentity(t *testing.T) {
	for name, mutate := range map[string]func(*api.Allocation){
		"task":    func(a *api.Allocation) { a.TaskID = "task2" },
		"service": func(a *api.Allocation) { a.ServiceID = "svc2" },
		"node":    func(a *api.Allocation) { a.NodeID = "node2" },
	} {
		t.Run(name, func(t *testing.T) {
			a := goodAlloc(api.Assignment{Kind: "SPO_RNG", ID: "hwrng"})
			mutate(&a)
			_, err := newResolver(&fakeLabeler{labels: swarmLabels()}, &fakeSource{alloc: a}).
				Resolve(context.Background(), ctrID, []string{"DOCKER_RESOURCE_SPO_RNG=hwrng"})
			if !errors.Is(err, ErrIdentityMismatch) {
				t.Fatalf("err = %v, want ErrIdentityMismatch", err)
			}
		})
	}
}

func TestFailsClosedWhenControllerUnavailable(t *testing.T) {
	boom := errors.New("dial: connection refused")
	_, err := newResolver(&fakeLabeler{labels: swarmLabels()}, &fakeSource{err: boom}).
		Resolve(context.Background(), ctrID, []string{"DOCKER_RESOURCE_SPO_RNG=hwrng"})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped controller error", err)
	}
}

func TestIgnoresUnconfiguredKindsAndRejectsBadDeviceNames(t *testing.T) {
	a := goodAlloc(api.Assignment{Kind: "SPO_RNG", ID: "hwrng"}, api.Assignment{Kind: "UNKNOWN", ID: "x"})
	plan, err := newResolver(&fakeLabeler{labels: swarmLabels()}, &fakeSource{alloc: a}).
		Resolve(context.Background(), ctrID, []string{"DOCKER_RESOURCE_SPO_RNG=hwrng"})
	if err != nil || !reflect.DeepEqual(plan.Devices, []string{"spo.local/rng=hwrng"}) {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}

	a = goodAlloc(api.Assignment{Kind: "SPO_RNG", ID: "../etc"})
	_, err = newResolver(&fakeLabeler{labels: swarmLabels()}, &fakeSource{alloc: a}).
		Resolve(context.Background(), ctrID, []string{"DOCKER_RESOURCE_SPO_RNG=x"})
	if err == nil {
		t.Fatal("accepted invalid CDI device name")
	}
}

func TestHintedKindsMatchesSwarmEnvFormat(t *testing.T) {
	got := hintedKinds([]string{"DOCKER_RESOURCE_SPO_SERIAL=ttyS1", "DOCKER_RESOURCE_SPO_RNG", "XDOCKER_RESOURCE_SPO_RNG=y"}, kinds)
	if !reflect.DeepEqual(got, []string{"SPO_SERIAL"}) {
		t.Fatalf("hintedKinds = %v", got)
	}
}
