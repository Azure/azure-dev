// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import "encoding/json"

// MaxStateStoreValueBytes is the preview service's 1 MiB serialized-value ceiling,
// documented as "1 MB" at:
// https://learn.microsoft.com/azure/foundry/agents/concepts/agent-state-store#service-limits
const MaxStateStoreValueBytes = 1024 * 1024

// StateStore describes an existing agent-scoped store. Timestamps are Unix seconds.
type StateStore struct {
	ID             string            `json:"id"`
	Object         string            `json:"object,omitempty"`
	Name           string            `json:"name"`
	UserIsolation  bool              `json:"user_isolation"`
	ItemTTLSeconds int64             `json:"item_ttl_seconds"`
	Description    *string           `json:"description"`
	Tags           map[string]string `json:"tags"`
	CreatedAt      int64             `json:"created_at"`
	UpdatedAt      int64             `json:"updated_at"`
}

// StateStoreItem contains item metadata and, on reads, its opaque JSON object value.
// Key listings and write responses can omit value or return null instead.
// RawMessage preserves numbers that cannot be represented by float64.
type StateStoreItem struct {
	ID        string            `json:"id"`
	Object    string            `json:"object,omitempty"`
	Key       string            `json:"key"`
	Value     json.RawMessage   `json:"value,omitempty"`
	Tags      map[string]string `json:"tags"`
	ETag      string            `json:"etag"`
	CreatedAt int64             `json:"created_at"`
	UpdatedAt int64             `json:"updated_at"`
}

// StateStorePage is a single service page. Pass LastID unchanged to the next list request;
// the service's cursor is distinct from the resource IDs in Data.
type StateStorePage[T any] struct {
	Object  string  `json:"object,omitempty"`
	Data    []T     `json:"data"`
	FirstID *string `json:"first_id"`
	LastID  *string `json:"last_id"`
	HasMore bool    `json:"has_more"`
}

// StateStoreListOptions controls a single list request; zero values use service defaults.
type StateStoreListOptions struct {
	Limit int
	Order string
	After string
}

// SetStateStoreItemRequest replaces the complete value and tags of an item.
// A nil or empty Tags map clears existing tags; it never merges them.
type SetStateStoreItemRequest struct {
	Value json.RawMessage   `json:"value"`
	Tags  map[string]string `json:"tags"`
}

// DeletedStateStoreItem is the service's deletion tombstone, including repeat deletions.
type DeletedStateStoreItem struct {
	ID      *string `json:"id"`
	Object  string  `json:"object,omitempty"`
	Key     string  `json:"key"`
	Deleted bool    `json:"deleted"`
}
