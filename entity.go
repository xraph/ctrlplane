package ctrlplane

import (
	"time"

	"github.com/xraph/ctrlplane/id"
)

// Entity is the base type embedded by all ctrlplane domain objects.
// It provides a unique identifier and creation/update timestamps.
type Entity struct {
	ID        id.ID     `db:"id"         json:"id"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// NewEntity creates an Entity with a fresh ID and UTC timestamps.
// The prefix determines the entity type encoded in the ID.
func NewEntity(prefix id.Prefix) Entity {
	now := time.Now().UTC()

	return Entity{
		ID:        id.New(prefix),
		CreatedAt: now,
		UpdatedAt: now,
	}
}
