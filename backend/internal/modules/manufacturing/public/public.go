// Package public exposes the manufacturing module's API surface.
// Other modules may only import this package, never internal sub-packages.
package public

// WorkOrder represents a manufacturing work order visible to other modules.
type WorkOrder struct {
	ID       string
	BranchID string
	Status   WorkOrderStatus
}

// WorkOrderStatus is the lifecycle state of a work order.
type WorkOrderStatus string

const (
	WorkOrderStatusDraft      WorkOrderStatus = "draft"
	WorkOrderStatusInProgress WorkOrderStatus = "in_progress"
	WorkOrderStatusCompleted  WorkOrderStatus = "completed"
	WorkOrderStatusCancelled  WorkOrderStatus = "cancelled"
)

// Service is the interface other modules use to interact with manufacturing.
type Service interface {
	GetWorkOrder(ctx interface{ Value(any) any }, id string) (*WorkOrder, error)
}
