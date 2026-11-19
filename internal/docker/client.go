// Package docker is a minimal Docker Engine API client for the endpoints SPO needs.
//
// It avoids the moby client module: two read-only endpoints do not justify its dependency tree.
package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/constructorfleet/SwarmPeripheralOperator/pkg/api"
)

// apiVersion is the oldest Engine API that exposes everything SPO reads (Engine 25).
const apiVersion = "v1.44"

// ErrNotFound reports a missing container or task.
var ErrNotFound = errors.New("not found")

var (
	containerIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	taskIDPattern      = regexp.MustCompile(`^[0-9a-z]{1,64}$`)
)

// Client talks to dockerd over its unix socket.
type Client struct {
	http *http.Client
}

// New returns a client for the daemon socket at path.
func New(socket string) *Client {
	dialer := &net.Dialer{}
	return &Client{http: &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socket)
			},
		},
	}}
}

// ContainerLabels returns labels for the container with exactly this ID.
//
// Uses the list endpoint: inspect blocks on the container lock that dockerd holds during runtime create.
func (c *Client) ContainerLabels(ctx context.Context, id string) (map[string]string, error) {
	if !containerIDPattern.MatchString(id) {
		return nil, fmt.Errorf("invalid container id %q", id)
	}
	filters, err := json.Marshal(map[string][]string{"id": {id}})
	if err != nil {
		return nil, err
	}
	q := url.Values{"all": {"1"}, "filters": {string(filters)}}
	var list []struct {
		ID     string            `json:"Id"`
		Labels map[string]string `json:"Labels"`
	}
	if err := c.get(ctx, "/containers/json?"+q.Encode(), &list); err != nil {
		return nil, err
	}
	// The id filter matches prefixes.
	for _, ctr := range list {
		if ctr.ID == id {
			return ctr.Labels, nil
		}
	}
	return nil, fmt.Errorf("container %s: %w", id, ErrNotFound)
}

// Task is the subset of a Swarm task SPO trusts. Only managers serve it.
type Task struct {
	ID           string
	ServiceID    string
	NodeID       string
	DesiredState string
	Version      uint64
	Named        []api.Assignment
}

// Task reads a Swarm task from a manager.
func (c *Client) Task(ctx context.Context, id string) (Task, error) {
	if !taskIDPattern.MatchString(id) {
		return Task{}, fmt.Errorf("invalid task id %q", id)
	}
	var raw struct {
		ID           string
		ServiceID    string
		NodeID       string
		DesiredState string
		Version      struct{ Index uint64 }
		// Swarm records the scheduler's per-task binding here, not in Spec.
		GenericResources []struct {
			NamedResourceSpec *struct{ Kind, Value string }
		}
	}
	if err := c.get(ctx, "/tasks/"+id, &raw); err != nil {
		return Task{}, err
	}
	t := Task{ID: raw.ID, ServiceID: raw.ServiceID, NodeID: raw.NodeID, DesiredState: raw.DesiredState, Version: raw.Version.Index}
	for _, r := range raw.GenericResources {
		if r.NamedResourceSpec != nil {
			t.Named = append(t.Named, api.Assignment{Kind: r.NamedResourceSpec.Kind, ID: r.NamedResourceSpec.Value})
		}
	}
	return t, nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/"+apiVersion+path, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%s: %w", path, ErrNotFound)
	case resp.StatusCode != http.StatusOK:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("docker %s: %s: %s", path, resp.Status, body)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}
