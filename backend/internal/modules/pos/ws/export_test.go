package ws

import (
	"time"

	"github.com/google/uuid"
)

// SetTimingForTest overrides the Hub's connection heartbeat/write timing so
// tests can exercise heartbeat-timeout disconnection without waiting out the
// production-scale defaultHeartbeatInterval/defaultHeartbeatTimeout.
func (h *Hub) SetTimingForTest(heartbeatInterval, heartbeatTimeout, writeTimeout time.Duration) {
	h.timing = timing{
		heartbeatInterval: heartbeatInterval,
		heartbeatTimeout:  heartbeatTimeout,
		writeTimeout:      writeTimeout,
	}
}

// RoomSizeForTest reports how many connections are currently registered for
// tenantID/branchID (0 if the room does not exist). Used to assert
// registration/unregistration and backpressure-drop behavior.
func (h *Hub) RoomSizeForTest(tenantID, branchID uuid.UUID) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms[roomKey{tenantID: tenantID, branchID: branchID}])
}
