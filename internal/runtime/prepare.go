package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	oci "github.com/opencontainers/runtime-spec/specs-go"
)

// Injector applies CDI devices to an OCI spec.
type Injector interface {
	Inject(spec *oci.Spec, devices []string) error
}

// Preparer edits a bundle before runc create.
type Preparer struct {
	Resolver *Resolver
	Injector Injector
	Audit    *slog.Logger
}

func readEnv(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // bundle path comes from the containerd shim
	if err != nil {
		return nil, err
	}
	var s struct {
		Process struct {
			Env []string `json:"env"`
		} `json:"process"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return s.Process.Env, nil
}

// Prepare resolves and injects devices into bundle/config.json. On error the bundle is untouched.
// Passthrough reads only process.env, so it cannot fail on spec fields SPO does not use.
func (p *Preparer) Prepare(ctx context.Context, bundle, containerID string) error {
	path := filepath.Join(bundle, "config.json")
	env, err := readEnv(path)
	if err != nil {
		return err
	}
	plan, err := p.Resolver.Resolve(ctx, containerID, env)
	if err == nil && plan.Passthrough {
		return nil
	}
	if err == nil {
		err = EditSpecFile(path, func(s *oci.Spec) error {
			if s.Linux == nil {
				s.Linux = &oci.Linux{}
			}
			return p.Injector.Inject(s, plan.Devices)
		})
	}
	if err != nil {
		p.Audit.LogAttrs(ctx, slog.LevelError, "device injection rejected", slog.String("decision", "reject"),
			slog.String("container", containerID), slog.String("task", plan.TaskID), slog.String("error", err.Error()))
		return err
	}
	p.Audit.LogAttrs(ctx, slog.LevelInfo, "devices injected", slog.String("decision", "inject"),
		slog.String("container", containerID), slog.String("task", plan.TaskID), slog.String("service", plan.ServiceID),
		slog.String("node", plan.NodeID), slog.Uint64("taskVersion", plan.Allocation.TaskVersion), slog.Any("devices", plan.Devices))
	return nil
}
