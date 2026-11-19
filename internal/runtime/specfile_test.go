package runtime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	oci "github.com/opencontainers/runtime-spec/specs-go"
)

const bundleSpec = `{"ociVersion":"1.3.0","x-future":{"a":1},
 "process":{"env":["A=1"],"cwd":"/","x-proc":true},
 "linux":{"resources":{"devices":[{"allow":false,"access":"rwm"}]},"x-linux":"keep"}}`

func writeBundle(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(bundleSpec), 0o640); err != nil { //nolint:gosec // mode preservation is under test
		t.Fatal(err)
	}
	return p
}

func TestEditSpecFilePreservesUnknownFields(t *testing.T) {
	p := writeBundle(t)
	err := EditSpecFile(p, func(s *oci.Spec) error {
		s.Process.Env = append(s.Process.Env, "B=2")
		s.Linux.Devices = append(s.Linux.Devices, oci.LinuxDevice{Path: "/dev/x", Type: "c", Major: 10, Minor: 183})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Future  map[string]int `json:"x-future"`
		Process struct {
			Env   []string `json:"env"`
			XProc bool     `json:"x-proc"`
		} `json:"process"`
		Linux struct {
			XLinux  string            `json:"x-linux"`
			Devices []oci.LinuxDevice `json:"devices"`
		} `json:"linux"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Future["a"] != 1 || !got.Process.XProc || got.Linux.XLinux != "keep" {
		t.Fatalf("unknown fields lost: %s", data)
	}
	if len(got.Process.Env) != 2 || len(got.Linux.Devices) != 1 {
		t.Fatalf("edits missing: %s", data)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640", fi.Mode().Perm())
	}
}

func TestEditSpecFileLeavesFileOnError(t *testing.T) {
	p := writeBundle(t)
	boom := errors.New("boom")
	if err := EditSpecFile(p, func(s *oci.Spec) error { s.Hostname = "x"; return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	data, _ := os.ReadFile(p) //nolint:gosec // test-controlled path
	if string(data) != bundleSpec {
		t.Fatalf("file changed on error: %s", data)
	}
	if entries, _ := os.ReadDir(filepath.Dir(p)); len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}
