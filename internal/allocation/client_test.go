package allocation

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/constructorfleet/SwarmPeripheralOperator/internal/pki"
	"github.com/constructorfleet/SwarmPeripheralOperator/pkg/api"
)

type fixture struct {
	srv   *httptest.Server
	cfg   ClientConfig
	calls atomic.Int32
}

func newFixture(t *testing.T, h func(*fixture, http.ResponseWriter, *http.Request)) *fixture {
	t.Helper()
	ca, err := pki.NewCA("ca")
	if err != nil {
		t.Fatal(err)
	}
	srvCert, err := ca.Issue("spo-controller", []string{"spo-controller"}, []net.IP{net.ParseIP("127.0.0.1")}, x509.ExtKeyUsageServerAuth)
	if err != nil {
		t.Fatal(err)
	}
	cli, err := ca.Issue("node1", nil, nil, x509.ExtKeyUsageClientAuth)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	f := &fixture{}
	f.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		h(f, w, r)
	}))
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	f.srv.TLS = &tls.Config{Certificates: []tls.Certificate{srvCert.TLS()}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, MinVersion: tls.VersionTLS13}
	f.srv.StartTLS()
	t.Cleanup(f.srv.Close)
	f.cfg = ClientConfig{
		URL: f.srv.URL, ServerName: "spo-controller",
		CAFile: write("ca.pem", ca.CertPEM()), CertFile: write("c.pem", cli.CertPEM), KeyFile: write("k.pem", cli.KeyPEM),
	}
	return f
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestAllocationSuccess(t *testing.T) {
	f := newFixture(t, func(_ *fixture, w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tasks/task1/allocation" {
			t.Errorf("path = %s", r.URL.Path)
		}
		reply(w, 200, api.Allocation{TaskID: "task1", NodeID: "node1", Assignments: []api.Assignment{{Kind: "SPO_RNG", ID: "hwrng"}}})
	})
	c, err := NewClient(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	a, err := c.Allocation(context.Background(), "task1")
	if err != nil || a.TaskID != "task1" || len(a.Assignments) != 1 {
		t.Fatalf("a=%+v err=%v", a, err)
	}
}

func TestAllocationClientErrorsAreNotRetried(t *testing.T) {
	f := newFixture(t, func(_ *fixture, w http.ResponseWriter, _ *http.Request) {
		reply(w, 403, api.Error{Code: api.CodeForbidden, Message: "task not on caller node"})
	})
	c, _ := NewClient(f.cfg)
	_, err := c.Allocation(context.Background(), "task1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != api.CodeForbidden {
		t.Fatalf("err = %v, want forbidden APIError", err)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", f.calls.Load())
	}
}

func TestAllocationRetriesUnavailable(t *testing.T) {
	f := newFixture(t, func(f *fixture, w http.ResponseWriter, _ *http.Request) {
		if f.calls.Load() < 3 {
			reply(w, 503, api.Error{Code: api.CodeUnavailable})
			return
		}
		reply(w, 200, api.Allocation{TaskID: "task1"})
	})
	c, _ := NewClient(f.cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Allocation(ctx, "task1"); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", f.calls.Load())
	}
}

func TestAllocationGivesUpAtDeadline(t *testing.T) {
	f := newFixture(t, func(_ *fixture, w http.ResponseWriter, _ *http.Request) {
		reply(w, 503, api.Error{Code: api.CodeUnavailable})
	})
	c, _ := NewClient(f.cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := c.Allocation(ctx, "task1"); err == nil {
		t.Fatal("expected error at deadline")
	}
}

func TestAllocationRejectsUntrustedServer(t *testing.T) {
	f := newFixture(t, func(_ *fixture, w http.ResponseWriter, _ *http.Request) { reply(w, 200, api.Allocation{}) })
	other, _ := pki.NewCA("other")
	p := filepath.Join(t.TempDir(), "other.pem")
	if err := os.WriteFile(p, other.CertPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	f.cfg.CAFile = p
	c, _ := NewClient(f.cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := c.Allocation(ctx, "task1"); err == nil {
		t.Fatal("trusted server signed by unknown CA")
	}
}

func TestAllocationRejectsInvalidTaskID(t *testing.T) {
	f := newFixture(t, func(_ *fixture, w http.ResponseWriter, _ *http.Request) { reply(w, 200, api.Allocation{}) })
	c, _ := NewClient(f.cfg)
	if _, err := c.Allocation(context.Background(), "../x"); err == nil {
		t.Fatal("accepted invalid task id")
	}
	if f.calls.Load() != 0 {
		t.Fatal("request sent for invalid task id")
	}
}
