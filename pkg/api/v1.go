// Package api defines the SPO controller wire contract (v1).
package api

// Assignment is one device the Swarm scheduler bound to a task.
// Kind is the Swarm generic resource kind; ID is the named resource value.
type Assignment struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Allocation is the controller's authoritative answer for one task.
// TaskVersion is the Swarm object version; it acts as a fencing token.
type Allocation struct {
	TaskID      string       `json:"taskId"`
	ServiceID   string       `json:"serviceId"`
	NodeID      string       `json:"nodeId"`
	TaskVersion uint64       `json:"taskVersion"`
	Assignments []Assignment `json:"assignments"`
}

// Error is the JSON body of a non-2xx controller response.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error codes returned by the controller.
const (
	CodeNotFound    = "not_found"
	CodeForbidden   = "forbidden"
	CodeNotRunnable = "not_runnable"
	CodeUnavailable = "unavailable"
	CodeBadRequest  = "bad_request"
)
