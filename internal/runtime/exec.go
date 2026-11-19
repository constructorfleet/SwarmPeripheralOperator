package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DefaultRuncPaths are searched when the config does not name runc.
var DefaultRuncPaths = []string{"/usr/bin/runc", "/usr/sbin/runc", "/usr/local/bin/runc", "/usr/local/sbin/runc"}

// WriteRuncError appends err to the runc log file. The containerd shim reports it as the create error.
func WriteRuncError(path, format string, err error) {
	if path == "" {
		return
	}
	f, openErr := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) //nolint:gosec // path from containerd shim
	if openErr != nil {
		return
	}
	defer func() { _ = f.Close() }()
	msg := "spo-runtime: " + err.Error()
	if format == "json" {
		_ = json.NewEncoder(f).Encode(map[string]string{"level": "error", "msg": msg, "time": time.Now().UTC().Format(time.RFC3339Nano)})
		return
	}
	_, _ = fmt.Fprintln(f, msg)
}

// FindRunc returns the configured runc or the first existing candidate that is not this binary.
func FindRunc(configured, self string, candidates []string) (string, error) {
	selfReal, _ := filepath.EvalSymlinks(self)
	if configured != "" {
		if real, err := filepath.EvalSymlinks(configured); err == nil && real == selfReal {
			return "", errors.New("configured runc is spo-runtime itself")
		}
		return configured, nil
	}
	for _, c := range candidates {
		real, err := filepath.EvalSymlinks(c)
		if err != nil || real == selfReal {
			continue
		}
		if fi, err := os.Stat(real); err == nil && fi.Mode().IsRegular() {
			return c, nil
		}
	}
	return "", errors.New("runc not found; set \"runc\" in the spo-runtime config")
}
