package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// ID is SpecWire's internal identifier.  Provider identifiers are stored in
// separate fields and are never interpreted as SpecWire IDs.
type ID string

func (id ID) String() string { return string(id) }

func (id ID) Empty() bool { return id == "" }

// NewID returns a UUID v4 without adding a third-party dependency to the
// domain package.  The textual representation is stable across all stores.
func NewID() ID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is not recoverable for an identifier.  Panic keeps
		// the failure at the boundary instead of silently creating collisions.
		panic(fmt.Sprintf("generate domain id: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return ID(fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	))
}

func requireID(name string, id ID) error {
	if id.Empty() {
		return fmt.Errorf("%w: %s is required", ErrInvalid, name)
	}
	return nil
}

func requireWorkspaceID(workspaceID ID) error {
	return requireID("workspace_id", workspaceID)
}
