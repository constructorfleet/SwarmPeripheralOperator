package version

import "testing"

func TestStringIncludesVersionAndCommit(t *testing.T) {
	old, oldCommit := Version, Commit
	t.Cleanup(func() { Version, Commit = old, oldCommit })

	Version, Commit = "1.2.3", "abc123"
	if got, want := String("spo-runtime"), "spo-runtime 1.2.3 (abc123)"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestDefaultsIdentifyDevBuild(t *testing.T) {
	if Version != "dev" || Commit != "unknown" {
		t.Fatalf("defaults = %q/%q, want dev/unknown", Version, Commit)
	}
}
