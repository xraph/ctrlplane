// Package pagination implements stable created-time and TypeID continuation.
package pagination

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
)

// Position is the last returned entity in descending creation order.
type Position struct {
	CreatedAt time.Time `json:"created_at"`
	ID        id.ID     `json:"id"`
}

// Decode validates a cursor. An empty cursor starts at the newest entity.
func Decode(cursor string) (Position, error) {
	if cursor == "" {
		return Position{}, nil
	}

	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(data) > 256 {
		return Position{}, fmt.Errorf("pagination cursor: %w", ctrlplane.ErrInvalidConfig)
	}

	stamp, rawID, ok := strings.Cut(string(data), "|")
	if !ok {
		return Position{}, fmt.Errorf("pagination cursor: %w", ctrlplane.ErrInvalidConfig)
	}

	created, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return Position{}, fmt.Errorf("pagination timestamp: %w", ctrlplane.ErrInvalidConfig)
	}

	target, err := id.Parse(rawID)
	if err != nil {
		return Position{}, fmt.Errorf("pagination identifier: %w", ctrlplane.ErrInvalidConfig)
	}

	return Position{CreatedAt: created.UTC(), ID: target}, nil
}

// Encode returns an opaque continuation for an entity.
func Encode(entity ctrlplane.Entity) string {
	return base64.RawURLEncoding.EncodeToString([]byte(entity.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + entity.ID.String()))
}

// Trim removes the lookahead row and reports continuation only when more rows exist.
func Trim[T any](items []T, limit int, key func(T) ctrlplane.Entity) ([]T, string) {
	if limit > 0 && len(items) > limit {
		return items[:limit], Encode(key(items[limit-1]))
	}

	return items, ""
}

// Page sorts an already scoped collection and applies continuation without requiring the anchor to still exist.
func Page[T any](items []T, cursor string, limit int, key func(T) ctrlplane.Entity) ([]T, string, int, error) {
	position, err := Decode(cursor)
	if err != nil {
		return nil, "", 0, err
	}

	slices.SortFunc(items, func(a, b T) int {
		left, right := key(a), key(b)
		if order := right.CreatedAt.Compare(left.CreatedAt); order != 0 {
			return order
		}

		return strings.Compare(right.ID.String(), left.ID.String())
	})

	total := len(items)
	if cursor != "" {
		start := len(items)
		for i, item := range items {
			entity := key(item)
			if entity.CreatedAt.Before(position.CreatedAt) || entity.CreatedAt.Equal(position.CreatedAt) && entity.ID.String() < position.ID.String() {
				start = i

				break
			}
		}

		items = items[start:]
	}

	if items == nil {
		items = make([]T, 0)
	}

	items, next := Trim(items, limit, key)

	return items, next, total, nil
}
