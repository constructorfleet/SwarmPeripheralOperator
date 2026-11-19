// Command spo-runtime is an OCI runtime wrapper. It injects SPO-allocated devices on create, then execs runc.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"

	oci "github.com/opencontainers/runtime-spec/specs-go"

	"github.com/constructorfleet/SwarmPeripheralOperator/internal/allocation"
	"github.com/constructorfleet/SwarmPeripheralOperator/internal/docker"
	"github.com/constructorfleet/SwarmPeripheralOperator/internal/ociargs"
	"github.com/constructorfleet/SwarmPeripheralOperator/internal/runtime"
	"github.com/constructorfleet/SwarmPeripheralOperator/internal/version"
	"github.com/constructorfleet/SwarmPeripheralOperator/pkg/api"
)

func main() {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--spo-version" {
		fmt.Println(version.String("spo-runtime"))
		return
	}
	inv := ociargs.Parse(args)

	cfgPath := os.Getenv("SPO_RUNTIME_CONFIG")
	if cfgPath == "" {
		cfgPath = runtime.DefaultConfigPath
	}
	cfg, err := runtime.LoadForCommand(cfgPath, inv.Command)
	if err == nil && inv.Command == "create" {
		err = prepare(cfg, inv)
	}
	if err != nil {
		runtime.WriteRuncError(inv.LogPath, inv.LogFormat, err)
		fmt.Fprintln(os.Stderr, "spo-runtime:", err)
		os.Exit(1)
	}

	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spo-runtime: locate self:", err)
		os.Exit(1)
	}
	runc, err := runtime.FindRunc(cfg.Runc, self, runtime.DefaultRuncPaths)
	if err != nil {
		runtime.WriteRuncError(inv.LogPath, inv.LogFormat, err)
		fmt.Fprintln(os.Stderr, "spo-runtime:", err)
		os.Exit(1)
	}
	//nolint:gosec // runc path is admin-configured or a fixed system location
	if err := syscall.Exec(runc, append([]string{runc}, args...), os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "spo-runtime: exec runc:", err)
		os.Exit(1)
	}
}

func prepare(cfg runtime.Config, inv ociargs.Invocation) error {
	if len(cfg.Kinds) == 0 {
		return nil
	}
	bundle := inv.Bundle
	if bundle == "" {
		bundle = "."
	}
	audit, closeAudit := auditLogger(cfg.AuditLog)
	defer closeAudit()

	ctx, cancel := context.WithTimeout(context.Background(), cfg.LookupTimeout.Duration)
	defer cancel()

	p := &runtime.Preparer{
		Resolver: &runtime.Resolver{
			Labels: docker.New(cfg.DockerSocket),
			Source: lazySource{cfg: cfg.Controller},
			Kinds:  cfg.Kinds,
		},
		Injector: lazyInjector{cfg: cfg},
		Audit:    audit,
	}
	return p.Prepare(ctx, bundle, inv.ContainerID)
}

// lazySource defers TLS setup until a container actually requests devices.
type lazySource struct{ cfg allocation.ClientConfig }

func (l lazySource) Allocation(ctx context.Context, taskID string) (api.Allocation, error) {
	c, err := allocation.NewClient(l.cfg)
	if err != nil {
		return api.Allocation{}, err
	}
	return c.Allocation(ctx, taskID)
}

// lazyInjector defers CDI spec loading until a container actually requests devices.
type lazyInjector struct{ cfg runtime.Config }

func (l lazyInjector) Inject(spec *oci.Spec, devices []string) error {
	inj, err := runtime.NewCDIInjector(l.cfg.CDISpecDirs, runtime.Policy{
		OwnerUID: 0, DevRoot: "/dev",
		AllowedHooks: l.cfg.AllowedHooks, AllowedMountPrefixes: l.cfg.AllowedMountPrefixes, Denied: l.cfg.Denied,
	})
	if err != nil {
		return err
	}
	return inj.Inject(spec, devices)
}

func auditLogger(path string) (*slog.Logger, func()) {
	var w io.Writer = os.Stderr
	closeFn := func() {}
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err == nil {
			if f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o640); err == nil { //nolint:gosec // admin-configured path
				w, closeFn = f, func() { _ = f.Close() }
			}
		}
	}
	return slog.New(slog.NewJSONHandler(w, nil)).With("component", "spo-runtime"), closeFn
}
