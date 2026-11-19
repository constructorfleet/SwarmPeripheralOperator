package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tags.cncf.io/container-device-interface/pkg/parser"

	"github.com/constructorfleet/SwarmPeripheralOperator/internal/allocation"
)

// DefaultConfigPath is read when SPO_RUNTIME_CONFIG is unset.
const DefaultConfigPath = "/etc/spo/runtime.json"

// Swarm kinds become env names, so restrict them to env-safe upper case.
var swarmKindPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// Duration decodes JSON strings such as "5s".
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	d.Duration = v
	return err
}

// Config is the node-local spo-runtime configuration.
type Config struct {
	Runc                 string                  `json:"runc"`
	DockerSocket         string                  `json:"dockerSocket"`
	Controller           allocation.ClientConfig `json:"controller"`
	Kinds                map[string]string       `json:"kinds"`
	CDISpecDirs          []string                `json:"cdiSpecDirs"`
	AllowedHooks         []string                `json:"allowedHooks"`
	AllowedMountPrefixes []string                `json:"allowedMountPrefixes"`
	Denied               []DeviceNumber          `json:"deniedDevices"`
	LookupTimeout        Duration                `json:"lookupTimeout"`
	AuditLog             string                  `json:"auditLog"`
}

// LoadConfig reads and validates path. A missing file yields a config with no SPO kinds.
func LoadConfig(path string) (Config, error) {
	cfg := Config{
		DockerSocket:  "/var/run/docker.sock",
		CDISpecDirs:   []string{"/etc/cdi", "/var/run/cdi"},
		LookupTimeout: Duration{10 * time.Second},
	}
	data, err := os.ReadFile(path) //nolint:gosec // admin-controlled config path
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, cfg.validate()
}

// LoadForCommand loads config strictly for create only. Other subcommands must keep
// working with a broken config so existing containers can be stopped and deleted.
func LoadForCommand(path, command string) (Config, error) {
	cfg, err := LoadConfig(path)
	if err != nil && command != "create" {
		return Config{}, nil
	}
	return cfg, err
}

func (c Config) validate() error {
	if c.Runc != "" && !filepath.IsAbs(c.Runc) {
		return fmt.Errorf("runc path %q must be absolute", c.Runc)
	}
	for swarmKind, cdiKind := range c.Kinds {
		if !swarmKindPattern.MatchString(swarmKind) {
			return fmt.Errorf("swarm kind %q must match %s", swarmKind, swarmKindPattern)
		}
		if !strings.Contains(cdiKind, "/") {
			return fmt.Errorf("CDI kind %q for %s must be vendor/class", cdiKind, swarmKind)
		}
		vendor, class := parser.ParseQualifier(cdiKind)
		if err := errors.Join(parser.ValidateVendorName(vendor), parser.ValidateClassName(class)); err != nil {
			return fmt.Errorf("CDI kind %q for %s is invalid: %w", cdiKind, swarmKind, err)
		}
	}
	if len(c.Kinds) > 0 {
		u, err := url.Parse(c.Controller.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("controller url %q must be https", c.Controller.URL)
		}
	}
	return nil
}
