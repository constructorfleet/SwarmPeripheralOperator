package ociargs

import (
	"reflect"
	"testing"
)

// Captured from containerd-shim-runc-v2 under Docker 29.10.
var shimCreate = []string{
	"--root", "/var/run/docker/runtime-runc/moby",
	"--log", "/run/x/log.json", "--log-format", "json",
	"create", "--bundle", "/run/x", "--pid-file", "/run/x/init.pid",
	"813d42e68dbc",
}

func TestParseShimCreate(t *testing.T) {
	got := Parse(shimCreate)
	want := Invocation{Command: "create", Bundle: "/run/x", ContainerID: "813d42e68dbc", LogPath: "/run/x/log.json", LogFormat: "json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %+v, want %+v", got, want)
	}
}

func TestParseEqualsFormAndShortBundle(t *testing.T) {
	got := Parse([]string{"--log=/l", "--systemd-cgroup", "create", "-b", "/b", "--no-pivot", "id1"})
	if got.Command != "create" || got.Bundle != "/b" || got.ContainerID != "id1" || got.LogPath != "/l" {
		t.Fatalf("Parse() = %+v", got)
	}
	got = Parse([]string{"create", "--bundle=/b2", "id2"})
	if got.Bundle != "/b2" || got.ContainerID != "id2" {
		t.Fatalf("Parse() = %+v", got)
	}
}

func TestParseCreateWithoutBundleDefaultsToEmpty(t *testing.T) {
	got := Parse([]string{"create", "id3"})
	if got.Command != "create" || got.Bundle != "" || got.ContainerID != "id3" {
		t.Fatalf("Parse() = %+v", got)
	}
}

func TestParseNonCreateCommands(t *testing.T) {
	cases := map[string][]string{
		"start":    {"--root", "/r", "start", "abc"},
		"features": {"features"},
		"":         {"--version"},
	}
	for want, args := range cases {
		if got := Parse(args).Command; got != want {
			t.Errorf("Parse(%v).Command = %q, want %q", args, got, want)
		}
	}
}

func TestParseCreateValueFlagsDoNotBecomeContainerID(t *testing.T) {
	got := Parse([]string{"create", "--bundle", "/b", "--console-socket", "/s", "--preserve-fds", "2", "real-id"})
	if got.ContainerID != "real-id" {
		t.Fatalf("ContainerID = %q, want real-id", got.ContainerID)
	}
}
