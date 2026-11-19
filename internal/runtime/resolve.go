// Package runtime decides which CDI devices a container being created may receive.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"tags.cncf.io/container-device-interface/pkg/parser"

	"github.com/constructorfleet/SwarmPeripheralOperator/internal/docker"
	"github.com/constructorfleet/SwarmPeripheralOperator/pkg/api"
)

// Labels written by the Swarm executor for its tasks. Image LABELs can set them on non-Swarm
// containers, so they are trusted only together with a Swarm-form container name.
const (
	LabelTaskID    = "com.docker.swarm.task.id"
	LabelServiceID = "com.docker.swarm.service.id"
	LabelNodeID    = "com.docker.swarm.node.id"
)

// Swarm exports assigned generic resources as DOCKER_RESOURCE_<KIND>=<ids>.
const resourceEnvPrefix = "DOCKER_RESOURCE_"

var (
	ErrNotSwarmTask     = errors.New("container requests SPO devices but is not a Swarm task")
	ErrIdentityMismatch = errors.New("controller allocation does not match container identity")
	ErrUnauthorized     = errors.New("task has no SPO assignment for requested kind")
)

// Labeler reads a local container from dockerd.
type Labeler interface {
	Container(ctx context.Context, containerID string) (docker.Container, error)
}

// AllocationSource returns the authoritative allocation for a Swarm task.
type AllocationSource interface {
	Allocation(ctx context.Context, taskID string) (api.Allocation, error)
}

// Resolver maps a container being created to the CDI devices it may receive.
type Resolver struct {
	Labels Labeler
	Source AllocationSource
	// Kinds maps Swarm generic resource kinds to CDI kinds (vendor/class).
	Kinds map[string]string
}

// Plan is the resolved decision for one container.
type Plan struct {
	Passthrough bool
	TaskID      string
	ServiceID   string
	NodeID      string
	Allocation  api.Allocation
	Devices     []string
}

// Resolve decides injection for a container. Env is untrusted and only selects whether to look up.
// On error the returned plan still carries known identity for audit.
// A Swarm assignment always adds its env entry, so a missing hint means no assignment exists.
func (r *Resolver) Resolve(ctx context.Context, containerID string, env []string) (Plan, error) {
	hinted := hintedKinds(env, r.Kinds)
	if len(hinted) == 0 {
		return Plan{Passthrough: true}, nil
	}

	ctr, err := r.Labels.Container(ctx, containerID)
	if err != nil {
		return Plan{}, fmt.Errorf("read container: %w", err)
	}
	labels := ctr.Labels
	plan := Plan{TaskID: labels[LabelTaskID], ServiceID: labels[LabelServiceID], NodeID: labels[LabelNodeID]}
	if plan.TaskID == "" || plan.ServiceID == "" || plan.NodeID == "" || !isSwarmTaskName(ctr.Name, plan.TaskID) {
		return Plan{}, ErrNotSwarmTask
	}

	alloc, err := r.Source.Allocation(ctx, plan.TaskID)
	if err != nil {
		return plan, fmt.Errorf("lookup allocation for task %s: %w", plan.TaskID, err)
	}
	if alloc.TaskID != plan.TaskID || alloc.ServiceID != plan.ServiceID || alloc.NodeID != plan.NodeID ||
		(alloc.ContainerID != "" && alloc.ContainerID != containerID) {
		return plan, fmt.Errorf("%w: task %s", ErrIdentityMismatch, plan.TaskID)
	}
	plan.Allocation = alloc

	assigned := map[string]bool{}
	for _, a := range alloc.Assignments {
		cdiKind, ok := r.Kinds[a.Kind]
		if !ok {
			continue
		}
		if err := parser.ValidateDeviceName(a.ID); err != nil {
			return plan, fmt.Errorf("assignment %s=%q: %w", a.Kind, a.ID, err)
		}
		assigned[a.Kind] = true
		plan.Devices = append(plan.Devices, cdiKind+"="+a.ID)
	}
	for _, k := range hinted {
		if !assigned[k] {
			return plan, fmt.Errorf("%w %s (task %s)", ErrUnauthorized, k, plan.TaskID)
		}
	}
	return plan, nil
}

// isSwarmTaskName reports whether a Docker name has the executor's form "/<service>.<slot|node>.<taskID>".
// Only Docker API callers can set names; images cannot.
func isSwarmTaskName(name, taskID string) bool {
	rest, ok := strings.CutPrefix(name, "/")
	if !ok || strings.Contains(rest, "/") {
		return false
	}
	prefix, ok := strings.CutSuffix(rest, "."+taskID)
	return ok && strings.Contains(prefix, ".")
}

// hintedKinds returns configured kinds named by DOCKER_RESOURCE_* env entries, sorted.
func hintedKinds(env []string, kinds map[string]string) []string {
	seen := map[string]bool{}
	for _, e := range env {
		name, _, ok := strings.Cut(e, "=")
		if !ok || !strings.HasPrefix(name, resourceEnvPrefix) {
			continue
		}
		suffix := strings.TrimPrefix(name, resourceEnvPrefix)
		for k := range kinds {
			if strings.ToUpper(k) == suffix {
				seen[k] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
