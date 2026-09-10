package model

// Status describes whether a board can enter a collaboration room.
type Status string

const (
	// StatusInitializing hides an imported board until its content is validated.
	StatusInitializing Status = "initializing"
	// StatusActive permits access subject to application authorization and entitlement.
	StatusActive Status = "active"
	// StatusDeleting closes access while application cleanup runs.
	StatusDeleting Status = "deleting"
)
