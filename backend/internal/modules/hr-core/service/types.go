package service

import "time"

// TerminateRequest carries the data needed to terminate an employee.
type TerminateRequest struct {
	TerminationDate time.Time
	Notes           string
}
