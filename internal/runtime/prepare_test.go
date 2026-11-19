package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	oci "github.com/opencontainers/runtime-spec/specs-go"

	"github.com/constructorfleet/SwarmPeripheralOperator/pkg/api"
)

type recordingInjector struct{ got []string }

func (r *recordingInjector) Inject(s *oci.Spec, devices []string) error {
	r.got = devices
	s.Linux.Devices = append(s.Linux.Devices, oci.LinuxDevice{Path: "/dev/hwrng", Type: "c", Major: 10, Minor: 183})
	return nil
}

func bundleWithEnv(t *testing.T, env ...string) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	data, err := json.Marshal(oci.Spec{Version: "1.3.0", Process: &oci.Process{Env: env}, Linux: &oci.Linux{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, data
}

func newPreparer(src *fakeSource, inj Injector, audit *bytes.Buffer) *Preparer {
	return &Preparer{
		Resolver: &Resolver{Labels: &fakeLabeler{labels: swarmLabels()}, Source: src, Kinds: kinds},
		Injector: inj,
		Audit:    slog.New(slog.NewJSONHandler(audit, nil)),
	}
}

func TestPreparePassthroughLeavesBundleUntouched(t *testing.T) {
	dir, orig := bundleWithEnv(t, "PATH=/bin")
	var audit bytes.Buffer
	if err := newPreparer(&fakeSource{}, &recordingInjector{}, &audit).Prepare(context.Background(), dir, ctrID); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "config.json")); !bytes.Equal(got, orig) { //nolint:gosec // test path
		t.Fatal("passthrough rewrote config.json")
	}
	if audit.Len() != 0 {
		t.Fatalf("passthrough audited: %s", audit.String())
	}
}

func TestPrepareInjectsAndAudits(t *testing.T) {
	dir, _ := bundleWithEnv(t, "DOCKER_RESOURCE_SPO_RNG=hwrng")
	inj := &recordingInjector{}
	var audit bytes.Buffer
	src := &fakeSource{alloc: goodAlloc(api.Assignment{Kind: "SPO_RNG", ID: "hwrng"})}
	if err := newPreparer(src, inj, &audit).Prepare(context.Background(), dir, ctrID); err != nil {
		t.Fatal(err)
	}
	var spec oci.Spec
	data, _ := os.ReadFile(filepath.Join(dir, "config.json")) //nolint:gosec // test path
	if err := json.Unmarshal(data, &spec); err != nil || len(spec.Linux.Devices) != 1 {
		t.Fatalf("devices not written: %s", data)
	}
	for _, want := range []string{`"decision":"inject"`, `"task":"task1"`, `"devices":["spo.local/rng=hwrng"]`, `"taskVersion":7`} {
		if !strings.Contains(audit.String(), want) {
			t.Errorf("audit missing %s: %s", want, audit.String())
		}
	}
}

func TestPrepareRejectionLeavesBundleAndAudits(t *testing.T) {
	dir, orig := bundleWithEnv(t, "DOCKER_RESOURCE_SPO_RNG=hwrng")
	var audit bytes.Buffer
	err := newPreparer(&fakeSource{alloc: goodAlloc()}, &recordingInjector{}, &audit).Prepare(context.Background(), dir, ctrID)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "config.json")); !bytes.Equal(got, orig) { //nolint:gosec // test path
		t.Fatal("rejection modified config.json")
	}
	if !strings.Contains(audit.String(), `"decision":"reject"`) {
		t.Fatalf("audit = %s", audit.String())
	}
}
