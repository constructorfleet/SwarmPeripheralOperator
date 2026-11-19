package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	oci "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
	"tags.cncf.io/container-device-interface/pkg/cdi"
	specs "tags.cncf.io/container-device-interface/specs-go"
)

// DeviceNumber identifies a Linux device by major and minor number.
type DeviceNumber struct {
	Major int64 `json:"major"`
	Minor int64 `json:"minor"`
}

// Always denied: /dev/mem, /dev/kmem, /dev/port expose host memory and I/O ports.
var builtinDenied = []DeviceNumber{{1, 1}, {1, 2}, {1, 4}}

// Policy restricts what admin-supplied CDI specs may do to a container.
type Policy struct {
	// OwnerUID must own spec dirs and files; 0 in production.
	OwnerUID uint32
	// DevRoot bounds host device paths; "/dev" in production.
	DevRoot              string
	AllowedHooks         []string
	AllowedMountPrefixes []string
	Denied               []DeviceNumber
}

// CDIInjector applies CDI devices to an OCI spec after policy validation.
type CDIInjector struct {
	cache  *cdi.Cache
	policy Policy
}

// NewCDIInjector loads CDI specs from trusted dirs. Missing dirs are skipped.
func NewCDIInjector(dirs []string, p Policy) (*CDIInjector, error) {
	for _, d := range dirs {
		if err := p.checkSpecDir(d); err != nil {
			return nil, err
		}
	}
	// No auto-refresh: the runtime is short-lived and must not start fsnotify watchers.
	cache, err := cdi.NewCache(cdi.WithSpecDirs(dirs...), cdi.WithAutoRefresh(false))
	if err != nil {
		return nil, fmt.Errorf("load CDI specs: %w", err)
	}
	return &CDIInjector{cache: cache, policy: p}, nil
}

// Inject validates every edit the named devices would apply, then applies them.
func (i *CDIInjector) Inject(spec *oci.Spec, devices []string) error {
	for _, name := range devices {
		d := i.cache.GetDevice(name)
		if d == nil {
			return fmt.Errorf("CDI device %s not found (spec errors: %v)", name, i.cache.GetErrors())
		}
		if err := i.policy.checkEdits(&d.GetSpec().ContainerEdits); err != nil {
			return fmt.Errorf("CDI spec %s: %w", d.GetSpec().GetPath(), err)
		}
		if err := i.policy.checkEdits(&d.ContainerEdits); err != nil {
			return fmt.Errorf("CDI device %s: %w", name, err)
		}
	}
	if _, err := i.cache.InjectDevices(spec, devices...); err != nil {
		return err
	}
	return nil
}

func (p Policy) checkSpecDir(dir string) error {
	if err := p.checkOwnership(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if ext != ".json" && ext != ".yaml" {
			continue
		}
		if err := p.checkOwnership(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (p Policy) checkOwnership(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&0o022 != 0 {
		return fmt.Errorf("CDI path %s is group or world writable", path)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != p.OwnerUID {
		return fmt.Errorf("CDI path %s is not owned by uid %d", path, p.OwnerUID)
	}
	return nil
}

func (p Policy) checkEdits(e *specs.ContainerEdits) error {
	if e == nil {
		return nil
	}
	if len(e.NetDevices) > 0 || e.IntelRdt != nil {
		return errors.New("netDevices and intelRdt edits are not permitted")
	}
	for _, h := range e.Hooks {
		if !slices.Contains(p.AllowedHooks, h.Path) {
			return fmt.Errorf("hook %q is not allowlisted", h.Path)
		}
	}
	for _, m := range e.Mounts {
		if err := p.checkMount(m); err != nil {
			return err
		}
	}
	for _, d := range e.DeviceNodes {
		if err := p.checkDeviceNode(d); err != nil {
			return err
		}
	}
	return nil
}

func (p Policy) checkMount(m *specs.Mount) error {
	if !isCleanAbs(m.ContainerPath) {
		return fmt.Errorf("mount container path %q is not clean and absolute", m.ContainerPath)
	}
	resolved, err := filepath.EvalSymlinks(m.HostPath)
	if err != nil || !isCleanAbs(m.HostPath) {
		return fmt.Errorf("mount host path %q is invalid", m.HostPath)
	}
	for _, prefix := range p.AllowedMountPrefixes {
		if within(m.HostPath, prefix) && within(resolved, prefix) {
			return nil
		}
	}
	return fmt.Errorf("mount host path %q is outside allowed prefixes", m.HostPath)
}

func (p Policy) checkDeviceNode(d *specs.DeviceNode) error {
	if !within(d.Path, "/dev") {
		return fmt.Errorf("device path %q must be under /dev", d.Path)
	}
	host := d.HostPath
	if host == "" {
		host = d.Path
	}
	if !within(host, p.DevRoot) {
		return fmt.Errorf("device host path %q must be under %s", host, p.DevRoot)
	}
	// Lstat: a symlink must not redirect to another device.
	var st unix.Stat_t
	if err := unix.Lstat(host, &st); err != nil {
		return fmt.Errorf("device host path %q: %w", host, err)
	}
	var typ string
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFCHR:
		typ = "c"
	case unix.S_IFBLK:
		typ = "b"
	default:
		return fmt.Errorf("device host path %q is not a device node", host)
	}
	num := deviceNumber(&st)
	if (d.Type != "" && d.Type != typ) || ((d.Major != 0 || d.Minor != 0) && (d.Major != num.Major || d.Minor != num.Minor)) {
		return fmt.Errorf("device %q spec does not match host node %s %d:%d", d.Path, typ, num.Major, num.Minor)
	}
	if slices.Contains(builtinDenied, num) || slices.Contains(p.Denied, num) {
		return fmt.Errorf("device %q (%d:%d) is denied", host, num.Major, num.Minor)
	}
	return nil
}

// deviceNumber decodes Rdev, which is int32 on darwin and uint64 on linux.
func deviceNumber(st *unix.Stat_t) DeviceNumber {
	rdev := uint64(st.Rdev) //nolint:gosec,unconvert // reinterprets kernel dev_t bits; width is platform-specific
	return DeviceNumber{Major: int64(unix.Major(rdev)), Minor: int64(unix.Minor(rdev))}
}

func isCleanAbs(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p
}

func within(path, root string) bool {
	return isCleanAbs(path) && (path == root || strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/"))
}
