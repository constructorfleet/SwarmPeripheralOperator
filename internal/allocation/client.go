// Package allocation fetches authoritative task allocations from spo-controller.
package allocation

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/constructorfleet/SwarmPeripheralOperator/pkg/api"
)

var taskIDPattern = regexp.MustCompile(`^[0-9a-z]{1,64}$`)

// ClientConfig configures mutual TLS to the controller.
type ClientConfig struct {
	URL        string `json:"url"`
	ServerName string `json:"serverName"`
	CAFile     string `json:"caFile"`
	CertFile   string `json:"certFile"`
	KeyFile    string `json:"keyFile"`
}

// APIError is a non-retryable controller rejection.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("controller: %d %s: %s", e.Status, e.Code, e.Message)
}

// Client queries the controller. Callers bound total time with the context.
type Client struct {
	base string
	http *http.Client
}

// NewClient builds an mTLS client. The server must chain to CAFile.
func NewClient(cfg ClientConfig) (*Client, error) {
	caPEM, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read controller CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("controller CA file has no certificates")
	}
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load client certificate: %w", err)
	}
	return &Client{base: cfg.URL, http: &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs: pool, Certificates: []tls.Certificate{cert}, ServerName: cfg.ServerName, MinVersion: tls.VersionTLS13,
		}},
	}}, nil
}

// Allocation returns the task's allocation, retrying transport failures and 503 until ctx ends.
func (c *Client) Allocation(ctx context.Context, taskID string) (api.Allocation, error) {
	if !taskIDPattern.MatchString(taskID) {
		return api.Allocation{}, fmt.Errorf("invalid task id %q", taskID)
	}
	backoff := 100 * time.Millisecond
	for {
		a, err := c.once(ctx, taskID)
		var apiErr *APIError
		if err == nil || (errors.As(err, &apiErr) && apiErr.Status != http.StatusServiceUnavailable) {
			return a, err
		}
		select {
		case <-ctx.Done():
			return api.Allocation{}, fmt.Errorf("%w (last error: %w)", ctx.Err(), err)
		case <-time.After(backoff):
		}
		backoff = min(2*backoff, time.Second)
	}
}

func (c *Client) once(ctx context.Context, taskID string) (api.Allocation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/tasks/"+taskID+"/allocation", http.NoBody)
	if err != nil {
		return api.Allocation{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return api.Allocation{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body := io.LimitReader(resp.Body, 1<<20)
	if resp.StatusCode != http.StatusOK {
		var apiErr api.Error
		_ = json.NewDecoder(body).Decode(&apiErr)
		return api.Allocation{}, &APIError{Status: resp.StatusCode, Code: apiErr.Code, Message: apiErr.Message}
	}
	var a api.Allocation
	if err := json.NewDecoder(body).Decode(&a); err != nil {
		return api.Allocation{}, fmt.Errorf("decode allocation: %w", err)
	}
	return a, nil
}
