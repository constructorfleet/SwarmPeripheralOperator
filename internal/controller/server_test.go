package controller

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/constructorfleet/SwarmPeripheralOperator/internal/allocation"
	"github.com/constructorfleet/SwarmPeripheralOperator/internal/docker"
	"github.com/constructorfleet/SwarmPeripheralOperator/internal/pki"
	"github.com/constructorfleet/SwarmPeripheralOperator/pkg/api"
)

type fakeTasks struct {
	task docker.Task
	err  error
}

func (f fakeTasks) Task(context.Context, string) (docker.Task, error) { return f.task, f.err }

func runningTask() docker.Task {
	return docker.Task{ID: "task1", ServiceID: "svc1", NodeID: "node1", DesiredState: "running", Version: 9, ContainerID: "c1",
		Named: []api.Assignment{{Kind: "SPO_RNG", ID: "hwrng"}, {Kind: "OTHER", ID: "x"}}}
}

func newServer(tasks TaskReader, audit *bytes.Buffer) *Server {
	return &Server{Tasks: tasks, Kinds: map[string]bool{"SPO_RNG": true}, Log: slog.New(slog.NewJSONHandler(audit, nil))}
}

func call(t *testing.T, s *Server, node, path string) (*httptest.ResponseRecorder, api.Allocation, api.Error) {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	if node != "" {
		req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{Subject: pkix.Name{CommonName: node}}}}
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var a api.Allocation
	var e api.Error
	if rec.Code == http.StatusOK {
		_ = json.Unmarshal(rec.Body.Bytes(), &a)
	} else {
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
	}
	return rec, a, e
}

func TestAllocationForOwningNodeFiltersKinds(t *testing.T) {
	var audit bytes.Buffer
	rec, a, _ := call(t, newServer(fakeTasks{task: runningTask()}, &audit), "node1", "/v1/tasks/task1/allocation")
	if rec.Code != 200 {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body)
	}
	if a.TaskID != "task1" || a.ServiceID != "svc1" || a.NodeID != "node1" || a.TaskVersion != 9 || a.ContainerID != "c1" {
		t.Fatalf("allocation = %+v", a)
	}
	if len(a.Assignments) != 1 || a.Assignments[0].Kind != "SPO_RNG" {
		t.Fatalf("assignments = %+v, want only SPO kinds", a.Assignments)
	}
	if !strings.Contains(audit.String(), `"caller":"node1"`) || !strings.Contains(audit.String(), `"decision":"allow"`) {
		t.Fatalf("audit = %s", audit.String())
	}
}

func TestAllocationErrors(t *testing.T) {
	stopped := runningTask()
	stopped.DesiredState = "shutdown"
	cases := map[string]struct {
		tasks TaskReader
		node  string
		code  int
		ecode string
	}{
		"no client cert": {fakeTasks{task: runningTask()}, "", 401, api.CodeForbidden},
		"other node":     {fakeTasks{task: runningTask()}, "node2", 403, api.CodeForbidden},
		"not running":    {fakeTasks{task: stopped}, "node1", 409, api.CodeNotRunnable},
		"unknown task":   {fakeTasks{err: docker.ErrNotFound}, "node1", 404, api.CodeNotFound},
		"docker down":    {fakeTasks{err: errors.New("dial unix: refused")}, "node1", 503, api.CodeUnavailable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var audit bytes.Buffer
			rec, _, e := call(t, newServer(tc.tasks, &audit), tc.node, "/v1/tasks/task1/allocation")
			if rec.Code != tc.code || e.Code != tc.ecode {
				t.Fatalf("code=%d err=%+v, want %d %s", rec.Code, e, tc.code, tc.ecode)
			}
			if strings.Contains(rec.Body.String(), "hwrng") {
				t.Fatal("rejection leaked assignment")
			}
		})
	}
}

func TestHealthz(t *testing.T) {
	rec, _, _ := call(t, newServer(fakeTasks{}, &bytes.Buffer{}), "", "/healthz")
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
}

// Contract test: runtime client against the real handler over mTLS.
func TestClientServerContractOverMTLS(t *testing.T) {
	ca, _ := pki.NewCA("ca")
	srvLeaf, _ := ca.Issue("spo-controller", []string{"spo-controller"}, []net.IP{net.ParseIP("127.0.0.1")}, x509.ExtKeyUsageServerAuth)
	cli, _ := ca.Issue("node1", nil, nil, x509.ExtKeyUsageClientAuth)
	dir := t.TempDir()
	write := func(n string, b []byte) string {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	caFile := write("ca.pem", ca.CertPEM())
	tlsCfg, err := ServerTLS(write("s.pem", srvLeaf.CertPEM), write("s.key", srvLeaf.KeyPEM), caFile)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(newServer(fakeTasks{task: runningTask()}, &bytes.Buffer{}).Handler())
	srv.TLS = tlsCfg
	srv.StartTLS()
	defer srv.Close()

	c, err := allocation.NewClient(allocation.ClientConfig{URL: srv.URL, ServerName: "spo-controller", CAFile: caFile,
		CertFile: write("c.pem", cli.CertPEM), KeyFile: write("c.key", cli.KeyPEM)})
	if err != nil {
		t.Fatal(err)
	}
	a, err := c.Allocation(context.Background(), "task1")
	if err != nil || len(a.Assignments) != 1 || a.Assignments[0].ID != "hwrng" {
		t.Fatalf("a=%+v err=%v", a, err)
	}
}
