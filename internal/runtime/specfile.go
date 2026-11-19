package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	oci "github.com/opencontainers/runtime-spec/specs-go"
)

// EditSpecFile applies fn to an OCI config.json and writes it back atomically.
// Fields unknown to the runtime-spec types survive; dockerd may emit a newer spec.
func EditSpecFile(path string, fn func(*oci.Spec) error) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path) //nolint:gosec // bundle path comes from the containerd shim
	if err != nil {
		return err
	}
	var spec oci.Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if err := fn(&spec); err != nil {
		return err
	}

	original, err := decodeGeneric(data)
	if err != nil {
		return err
	}
	typed, err := json.Marshal(&spec)
	if err != nil {
		return err
	}
	edited, err := decodeGeneric(typed)
	if err != nil {
		return err
	}
	out, err := json.Marshal(merge(original, edited))
	if err != nil {
		return err
	}
	return writeAtomic(path, out, fi.Mode().Perm())
}

// decodeGeneric keeps numbers exact; float64 would corrupt large int64 limits.
func decodeGeneric(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	return v, err
}

// merge overlays src onto dst. Objects merge by key; other values replace.
func merge(dst, src any) any {
	d, dok := dst.(map[string]any)
	s, sok := src.(map[string]any)
	if !dok || !sok {
		return src
	}
	for k, v := range s {
		d[k] = merge(d[k], v)
	}
	return d
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
