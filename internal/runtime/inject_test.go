package runtime

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	oci "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

func writeSpecDir(t *testing.T, spec string) string {
	t.Helper()
	dir := t.TempDir()
	// Explicit modes: t.TempDir honors the process umask.
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // directories need the execute bit
		t.Fatal(err)
	}
	p := filepath.Join(dir, "spo.json")
	if err := os.WriteFile(p, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func basePolicy() Policy {
	return Policy{OwnerUID: uint32(os.Getuid()), DevRoot: "/dev"} //nolint:gosec // uids fit in uint32
}

func newInjector(t *testing.T, spec string, p Policy) *CDIInjector {
	t.Helper()
	inj, err := NewCDIInjector([]string{writeSpecDir(t, spec)}, p)
	if err != nil {
		t.Fatal(err)
	}
	return inj
}

const nullSpec = `{"cdiVersion":"0.6.0","kind":"spo.local/rng","devices":[
  {"name":"hwrng","containerEdits":{"deviceNodes":[{"path":"/dev/null"}]}}]}`

func TestInjectAddsDeviceNodeAndCgroupRule(t *testing.T) {
	spec := &oci.Spec{Process: &oci.Process{}, Linux: &oci.Linux{}}
	if err := newInjector(t, nullSpec, basePolicy()).Inject(spec, []string{"spo.local/rng=hwrng"}); err != nil {
		t.Fatal(err)
	}
	if len(spec.Linux.Devices) != 1 || spec.Linux.Devices[0].Path != "/dev/null" {
		t.Fatalf("devices = %+v", spec.Linux.Devices)
	}
	var st unix.Stat_t
	if err := unix.Stat("/dev/null", &st); err != nil {
		t.Fatal(err)
	}
	rules := spec.Linux.Resources.Devices
	if len(rules) != 1 || !rules[0].Allow || *rules[0].Major != deviceNumber(&st).Major {
		t.Fatalf("cgroup rules = %+v", rules)
	}
}

func TestInjectUnknownDeviceFails(t *testing.T) {
	err := newInjector(t, nullSpec, basePolicy()).Inject(&oci.Spec{}, []string{"spo.local/rng=missing"})
	if err == nil {
		t.Fatal("injected unknown device")
	}
}

// allowedLib is an allowlisted mount prefix that exists on every test host.
const allowedLib = "/usr"

func allowLib(p *Policy) { p.AllowedMountPrefixes = []string{allowedLib} }

func TestPolicyRejections(t *testing.T) {
	var st unix.Stat_t
	if err := unix.Stat("/dev/null", &st); err != nil {
		t.Fatal(err)
	}
	major, minor := deviceNumber(&st).Major, deviceNumber(&st).Minor
	cases := map[string]struct {
		want   string
		edits  string
		policy func(*Policy)
	}{
		"hook off allowlist":     {want: "not allowlisted", edits: `"hooks":[{"hookName":"createContainer","path":"/usr/bin/evil"}]`},
		"container path outside": {want: "must be under /dev", edits: `"deviceNodes":[{"path":"/tmp/x","hostPath":"/dev/null"}]`},
		"host path outside":      {want: "must be under /dev", edits: `"deviceNodes":[{"path":"/dev/x","hostPath":"/etc/passwd"}]`},
		"host path traversal":    {want: "must be under /dev", edits: `"deviceNodes":[{"path":"/dev/x","hostPath":"/dev/../etc/passwd"}]`},
		"major mismatch":         {want: "does not match host", edits: `"deviceNodes":[{"path":"/dev/null","type":"c","major":` + itoa(major+1) + `,"minor":` + itoa(minor) + `}]`},
		"denied device":          {want: "is denied", edits: `"deviceNodes":[{"path":"/dev/null"}]`, policy: func(p *Policy) { p.Denied = []DeviceNumber{{Major: major, Minor: minor}} }},
		"mount not allowed":      {want: "outside allowed prefixes", edits: `"mounts":[{"hostPath":"/etc","containerPath":"/host-etc"}]`},
		"mount type overlay":     {want: "mount type", edits: `"mounts":[{"hostPath":"` + allowedLib + `","containerPath":"/x","type":"overlay","options":["lowerdir=/etc"]}]`, policy: allowLib},
		"mount option":           {want: "mount option", edits: `"mounts":[{"hostPath":"` + allowedLib + `","containerPath":"/x","options":["rbind","suid"]}]`, policy: allowLib},
		"net device":             {want: "not permitted", edits: `"netDevices":[{"hostInterfaceName":"eth0","name":"eth1"}]`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := basePolicy()
			if tc.policy != nil {
				tc.policy(&p)
			}
			spec := `{"cdiVersion":"1.1.0","kind":"spo.local/rng","devices":[{"name":"d","containerEdits":{` + tc.edits + `}}]}`
			err := newInjector(t, spec, p).Inject(&oci.Spec{}, []string{"spo.local/rng=d"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPolicyRejectsSymlinkAndRegularFileHostPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/null", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	p := basePolicy()
	p.DevRoot = root
	for _, name := range []string{"file", "link"} {
		hp := filepath.Join(root, name)
		spec := `{"cdiVersion":"0.6.0","kind":"spo.local/rng","devices":[{"name":"d","containerEdits":{"deviceNodes":[{"path":"/dev/x","hostPath":"` + hp + `"}]}}]}`
		err := newInjector(t, spec, p).Inject(&oci.Spec{}, []string{"spo.local/rng=d"})
		if err == nil || !strings.Contains(err.Error(), "not a device node") {
			t.Errorf("%s: err = %v, want non-device rejection", name, err)
		}
	}
}

func TestPolicyAllowsListedHooksAndMounts(t *testing.T) {
	lib, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := basePolicy()
	p.AllowedHooks = []string{"/usr/bin/nvidia-cdi-hook"}
	p.AllowedMountPrefixes = []string{lib}
	spec := `{"cdiVersion":"0.6.0","kind":"spo.local/rng",
	  "containerEdits":{"hooks":[{"hookName":"createContainer","path":"/usr/bin/nvidia-cdi-hook"}]},
	  "devices":[{"name":"d","containerEdits":{"mounts":[{"hostPath":"` + lib + `","containerPath":"/lib/x","type":"bind","options":["ro","nosuid","nodev","rbind","rprivate"]}],"deviceNodes":[{"path":"/dev/null"}]}}]}`
	s := &oci.Spec{}
	if err := newInjector(t, spec, p).Inject(s, []string{"spo.local/rng=d"}); err != nil {
		t.Fatal(err)
	}
	if s.Hooks == nil || len(s.Hooks.CreateContainer) != 1 || len(s.Mounts) != 1 {
		t.Fatalf("hooks=%+v mounts=%+v", s.Hooks, s.Mounts)
	}
}

// Regression (#27): on usr-merged hosts /lib -> /usr/lib, so a mount path and its
// target sit under different allowed prefixes.
func TestPolicyMountPathAndTargetMayUseDifferentPrefixes(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(root, "usr", "lib", "firmware")
	if err := os.MkdirAll(real, 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "lib")
	if err := os.Symlink(filepath.Join(root, "usr", "lib"), link); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "etc")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(real, "escape")); err != nil {
		t.Fatal(err)
	}

	mountSpec := func(host string) string {
		return `{"cdiVersion":"0.6.0","kind":"spo.local/rng","devices":[{"name":"d","containerEdits":{
		  "mounts":[{"hostPath":"` + host + `","containerPath":"/lib/firmware","options":["ro","rbind"]}],
		  "deviceNodes":[{"path":"/dev/null"}]}}]}`
	}
	p := basePolicy()
	p.AllowedMountPrefixes = []string{filepath.Join(link, "firmware"), real}
	if err := newInjector(t, mountSpec(filepath.Join(link, "firmware")), p).Inject(&oci.Spec{}, []string{"spo.local/rng=d"}); err != nil {
		t.Fatalf("symlinked dir with both sides allowlisted rejected: %v", err)
	}

	err = newInjector(t, mountSpec(filepath.Join(real, "escape")), p).Inject(&oci.Spec{}, []string{"spo.local/rng=d"})
	if err == nil || !strings.Contains(err.Error(), "outside allowed prefixes") {
		t.Fatalf("err = %v, want rejection when the target leaves every prefix", err)
	}
}

func TestPolicyValidatesSpecLevelEdits(t *testing.T) {
	spec := `{"cdiVersion":"0.6.0","kind":"spo.local/rng",
	  "containerEdits":{"hooks":[{"hookName":"createContainer","path":"/usr/bin/evil"}]},
	  "devices":[{"name":"d","containerEdits":{"deviceNodes":[{"path":"/dev/null"}]}}]}`
	err := newInjector(t, spec, basePolicy()).Inject(&oci.Spec{}, []string{"spo.local/rng=d"})
	if err == nil || !strings.Contains(err.Error(), "CDI spec") || !strings.Contains(err.Error(), "not allowlisted") {
		t.Fatalf("err = %v, want spec-level hook rejection", err)
	}
}

func TestNewInjectorRejectsUntrustedSpecDirs(t *testing.T) {
	dir := writeSpecDir(t, nullSpec)
	if err := os.Chmod(dir, 0o777); err != nil { //nolint:gosec // test sets unsafe mode on purpose
		t.Fatal(err)
	}
	if _, err := NewCDIInjector([]string{dir}, basePolicy()); err == nil || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("err = %v, want writable rejection", err)
	}

	dir = writeSpecDir(t, nullSpec)
	p := basePolicy()
	p.OwnerUID++
	if _, err := NewCDIInjector([]string{dir}, p); err == nil || !strings.Contains(err.Error(), "owned") {
		t.Fatalf("err = %v, want owner rejection", err)
	}
}

func TestNewInjectorSkipsMissingSpecDirs(t *testing.T) {
	if _, err := NewCDIInjector([]string{filepath.Join(t.TempDir(), "absent")}, basePolicy()); err != nil {
		t.Fatalf("err = %v, want missing dir tolerated", err)
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }
