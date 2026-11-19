package runtime

import (
	"context"
	"errors"
	"log/slog"
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

var errUnchanged = errors.New("unchanged")

// Prepare resolves and injects devices into bundle/config.json. On error the bundle is untouched.
func (p *Preparer) Prepare(ctx context.Context, bundle, containerID string) error {
	var plan Plan
	err := EditSpecFile(filepath.Join(bundle, "config.json"), func(s *oci.Spec) error {
		var env []string
		if s.Process != nil {
			env = s.Process.Env
		}
		var err error
		if plan, err = p.Resolver.Resolve(ctx, containerID, env); err != nil {
			return err
		}
		if plan.Passthrough {
			return errUnchanged
		}
		if s.Linux == nil {
			s.Linux = &oci.Linux{}
		}
		return p.Injector.Inject(s, plan.Devices)
	})
	switch {
	case errors.Is(err, errUnchanged):
		return nil
	case err != nil:
		p.Audit.LogAttrs(ctx, slog.LevelError, "device injection rejected", slog.String("decision", "reject"),
			slog.String("container", containerID), slog.String("task", plan.TaskID), slog.String("error", err.Error()))
		return err
	}
	p.Audit.LogAttrs(ctx, slog.LevelInfo, "devices injected", slog.String("decision", "inject"),
		slog.String("container", containerID), slog.String("task", plan.TaskID), slog.String("service", plan.ServiceID),
		slog.String("node", plan.NodeID), slog.Uint64("taskVersion", plan.Allocation.TaskVersion), slog.Any("devices", plan.Devices))
	return nil
}
