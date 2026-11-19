package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadConfigAppliesDefaults(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, `{"runc":"/usr/bin/runc","kinds":{"SPO_RNG":"spo.local/rng"},
	  "controller":{"url":"https://c:7443","caFile":"/ca","certFile":"/c","keyFile":"/k"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DockerSocket != "/var/run/docker.sock" || cfg.LookupTimeout.Duration != 10*time.Second {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	if len(cfg.CDISpecDirs) != 2 || cfg.CDISpecDirs[0] != "/etc/cdi" {
		t.Fatalf("cdi dirs = %v", cfg.CDISpecDirs)
	}
}

func TestLoadConfigMissingFileIsNotAnError(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil || len(cfg.Kinds) != 0 {
		t.Fatalf("cfg=%+v err=%v, want empty config", cfg, err)
	}
}

func TestLoadConfigRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"bad json":       `{`,
		"unknown field":  `{"runc":"/r","kindz":{}}`,
		"bad cdi kind":   `{"runc":"/r","kinds":{"SPO_RNG":"not a kind"},"controller":{"url":"https://c"}}`,
		"bad swarm kind": `{"runc":"/r","kinds":{"spo rng":"spo.local/rng"},"controller":{"url":"https://c"}}`,
		"no controller":  `{"runc":"/r","kinds":{"SPO_RNG":"spo.local/rng"}}`,
		"http":           `{"runc":"/r","kinds":{"SPO_RNG":"spo.local/rng"},"controller":{"url":"http://c"}}`,
		"bad timeout":    `{"runc":"/r","lookupTimeout":"soon"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(writeConfig(t, body)); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
}

func TestLoadConfigRelativeRuncRejected(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, `{"runc":"runc"}`))
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("err = %v", err)
	}
}
