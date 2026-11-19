package runtime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRuncErrorJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log.json")
	WriteRuncError(p, "json", errors.New("spo: denied"))
	data, err := os.ReadFile(p) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	var entry struct{ Level, Msg, Time string }
	if err := json.Unmarshal(data, &entry); err != nil || entry.Level != "error" || entry.Msg != "spo-runtime: spo: denied" || entry.Time == "" {
		t.Fatalf("entry = %s (err %v)", data, err)
	}
}

func TestWriteRuncErrorTextAndEmptyPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	WriteRuncError(p, "", errors.New("x"))
	if data, _ := os.ReadFile(p); !strings.Contains(string(data), "spo-runtime: x") { //nolint:gosec // test path
		t.Fatalf("text log = %q", data)
	}
	WriteRuncError("", "json", errors.New("x")) // must not panic
}

func TestFindRuncSkipsSelfAndMissing(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "self")
	real := filepath.Join(dir, "runc")
	for _, p := range []string{self, real} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o700); err != nil { //nolint:gosec // executable fixture
			t.Fatal(err)
		}
	}
	got, err := FindRunc("", self, []string{filepath.Join(dir, "absent"), self, real})
	if err != nil || got != real {
		t.Fatalf("FindRunc = %q, %v", got, err)
	}
	if got, _ := FindRunc("/opt/runc", self, nil); got != "/opt/runc" {
		t.Fatalf("configured path ignored: %q", got)
	}
	if _, err := FindRunc("", self, []string{self}); err == nil {
		t.Fatal("found only self")
	}
}
