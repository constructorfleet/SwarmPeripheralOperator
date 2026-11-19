// Package controller serves authoritative Swarm task allocations to node runtimes.
package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/constructorfleet/SwarmPeripheralOperator/internal/docker"
	"github.com/constructorfleet/SwarmPeripheralOperator/pkg/api"
)

// TaskReader reads tasks from a Swarm manager.
type TaskReader interface {
	Task(ctx context.Context, id string) (docker.Task, error)
}

// Server answers allocation lookups. The client certificate CN is the caller's Swarm node ID.
type Server struct {
	Tasks TaskReader
	Kinds map[string]bool
	Log   *slog.Logger
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/tasks/{id}/allocation", s.allocation)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return mux
}

func (s *Server) allocation(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	log := s.Log.With("task", taskID)
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		deny(w, log, http.StatusUnauthorized, api.CodeForbidden, "client certificate required")
		return
	}
	caller := r.TLS.PeerCertificates[0].Subject.CommonName
	log = log.With("caller", caller)

	task, err := s.Tasks.Task(r.Context(), taskID)
	switch {
	case errors.Is(err, docker.ErrNotFound):
		deny(w, log, http.StatusNotFound, api.CodeNotFound, "task not found")
		return
	case err != nil:
		log.Error("task lookup failed", "decision", "error", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, api.Error{Code: api.CodeUnavailable, Message: "swarm manager unavailable"})
		return
	}
	// Only the node running the task may learn its devices.
	if task.NodeID != caller {
		deny(w, log, http.StatusForbidden, api.CodeForbidden, "task is not assigned to caller node")
		return
	}
	// Stale or replaced tasks must not receive devices.
	if task.DesiredState != "running" {
		deny(w, log, http.StatusConflict, api.CodeNotRunnable, "task desired state is "+task.DesiredState)
		return
	}

	out := api.Allocation{TaskID: task.ID, ServiceID: task.ServiceID, NodeID: task.NodeID, TaskVersion: task.Version, ContainerID: task.ContainerID, Assignments: []api.Assignment{}}
	for _, a := range task.Named {
		if s.Kinds[a.Kind] {
			out.Assignments = append(out.Assignments, a)
		}
	}
	log.Info("allocation served", "decision", "allow", "service", task.ServiceID, "taskVersion", task.Version, "assignments", out.Assignments)
	writeJSON(w, http.StatusOK, out)
}

func deny(w http.ResponseWriter, log *slog.Logger, status int, code, msg string) {
	log.Warn("allocation denied", "decision", "deny", "code", code, "reason", msg)
	writeJSON(w, status, api.Error{Code: code, Message: msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ServerTLS requires TLS 1.3 and client certificates signed by clientCA.
func ServerTLS(certFile, keyFile, clientCAFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(clientCAFile) //nolint:gosec // operator-supplied path
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("client CA file has no certificates")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS13,
	}, nil
}
