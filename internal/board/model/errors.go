// Package model defines project and board metadata without cross-domain dependencies.
package model

import "errors"

var (
	// ErrPermissionDenied indicates a database policy rejected a write.
	ErrPermissionDenied = errors.New("permission denied")
	// ErrInvalidName indicates a missing or invalid UTF-8 name.
	ErrInvalidName = errors.New("invalid name")
	// ErrInvalidRevision indicates an absent or exhausted metadata revision.
	ErrInvalidRevision = errors.New("invalid revision")
	// ErrInvalidState indicates inconsistent persisted metadata or a forbidden lifecycle transition.
	ErrInvalidState = errors.New("invalid resource state")
	// ErrNotFound deliberately covers both missing and inaccessible rows.
	ErrNotFound = errors.New("resource not found")
	// ErrRevisionConflict means the caller must reload before changing the resource.
	ErrRevisionConflict = errors.New("revision conflict")
	// ErrProjectNotEmpty prevents deleting a project that still owns boards.
	ErrProjectNotEmpty = errors.New("project not empty")
	// ErrInvalidProject indicates a missing, deleted, or cross-workspace project assignment.
	ErrInvalidProject = errors.New("invalid project assignment")
	// ErrInvalidPage indicates an invalid list limit or cursor position.
	ErrInvalidPage = errors.New("invalid page")
)
