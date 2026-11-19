package docker

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	cid      = "813d42e68dbc6a5dc25de74c49e11bde933e41bc066452f1d0439dabbbe63b20"
	cidOther = "813d42e68dbc6a5dc25de74c49e11bde933e41bc066452f1d0439dabbbe63b21"
)

// serve starts an HTTP server on a short unix socket path (macOS caps sun_path at 104 bytes).
func serve(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "spo")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return New(sock)
}

func TestContainerLabelsRequiresExactIDMatch(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.44/containers/json" || r.URL.Query().Get("all") != "1" {
			t.Errorf("unexpected request %s", r.URL)
		}
		if !strings.Contains(r.URL.Query().Get("filters"), cid) {
			t.Errorf("filters = %q", r.URL.Query().Get("filters"))
		}
		_, _ = w.Write([]byte(`[{"Id":"` + cidOther + `","Names":["/a"],"Labels":{"x":"wrong"}},{"Id":"` + cid + `","Names":["/svc.1.t"],"Labels":{"x":"right"}}]`))
	})
	ctr, err := c.Container(context.Background(), cid)
	if err != nil {
		t.Fatal(err)
	}
	if ctr.Labels["x"] != "right" || ctr.Name != "/svc.1.t" {
		t.Fatalf("container = %+v, want exact-ID container", ctr)
	}
}

func TestContainerLabelsNotFound(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"Id":"` + cidOther + `"}]`))
	})
	if _, err := c.Container(context.Background(), cid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRejectsMalformedIDsWithoutRequest(t *testing.T) {
	c := serve(t, func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	})
	for _, id := range []string{"", "../../info", "ABC", strings.Repeat("a", 65)} {
		if _, err := c.Container(context.Background(), id); err == nil {
			t.Errorf("Container(%q) succeeded", id)
		}
		if _, err := c.Task(context.Background(), id); err == nil {
			t.Errorf("Task(%q) succeeded", id)
		}
	}
}

func TestTaskParsesAssignedGenericResources(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.44/tasks/xiq3arhllh8u9g9z67bcz7m0s" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ID":"xiq3arhllh8u9g9z67bcz7m0s","ServiceID":"svc1","NodeID":"node1",
			"DesiredState":"running","Version":{"Index":42},
			"Status":{"ContainerStatus":{"ContainerID":"abc"}},
			"GenericResources":[{"NamedResourceSpec":{"Kind":"SPO_RNG","Value":"hwrng"}},
			                    {"DiscreteResourceSpec":{"Kind":"SPO_MEM","Value":2}}]}`))
	})
	task, err := c.Task(context.Background(), "xiq3arhllh8u9g9z67bcz7m0s")
	if err != nil {
		t.Fatal(err)
	}
	if task.ServiceID != "svc1" || task.NodeID != "node1" || task.DesiredState != "running" || task.Version != 42 || task.ContainerID != "abc" {
		t.Fatalf("task = %+v", task)
	}
	if len(task.Named) != 1 || task.Named[0].Kind != "SPO_RNG" || task.Named[0].ID != "hwrng" {
		t.Fatalf("named = %+v, want only named resources", task.Named)
	}
}

func TestTaskNotFound(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"no such task"}`, http.StatusNotFound)
	})
	if _, err := c.Task(context.Background(), "abc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestServerErrorIsReported(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	if _, err := c.Task(context.Background(), "abc"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want non-NotFound error", err)
	}
}
