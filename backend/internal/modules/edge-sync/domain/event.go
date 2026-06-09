// Package domain holds value types shared across edge-sync sub-packages.
package domain

import "time"

// ConnState is the local server's cloud connectivity state.
type ConnState int

const (
	ConnStateOnline   ConnState = iota // cloud reachable, low latency
	ConnStateDegraded                  // cloud reachable, high latency
	ConnStateOffline                   // cloud unreachable
	ConnStateSyncing                   // just reconnected, draining backlog
)

// String returns a human-readable label for the state (used in logs and API responses).
func (s ConnState) String() string {
	switch s {
	case ConnStateOnline:
		return "ONLINE"
	case ConnStateDegraded:
		return "DEGRADED"
	case ConnStateOffline:
		return "OFFLINE"
	case ConnStateSyncing:
		return "SYNCING"
	default:
		return "UNKNOWN"
	}
}

// OutboxEvent is a locally-generated domain event pending cloud delivery.
type OutboxEvent struct {
	ID            string
	EventType     string
	AggregateType string
	AggregateID   string
	Payload       string // JSON string
	CreatedAt     time.Time
}

// InboxEvent is a cloud-delivered event pending local application.
type InboxEvent struct {
	ID         string
	EventType  string
	Payload    string // JSON string
	ReceivedAt time.Time
}
