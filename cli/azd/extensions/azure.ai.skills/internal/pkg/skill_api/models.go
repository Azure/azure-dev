// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package skill_api provides a typed REST client for the Foundry Skills
// data-plane surface, plus helpers for parsing SKILL.md files and safely
// extracting downloaded skill packages.
//
// The wire shapes mirror the versioned Skills API from
// azure-rest-api-specs (Foundry data-plane, Skills V1Preview):
// each skill has 1+ immutable versions, and skill content lives on a
// SkillVersion (via inline_content or uploaded files), not on the Skill
// resource itself.
package skill_api

// Skill is the top-level skill resource. Content lives on a SkillVersion,
// so this struct intentionally does not carry description/instructions
// other than the human-readable label.
type Skill struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	CreatedAt      int64  `json:"created_at"`
	DefaultVersion string `json:"default_version"`
	LatestVersion  string `json:"latest_version"`
}

// SkillVersion is an immutable version of a skill. The wire response does
// not echo back inline_content / files, only the version envelope.
type SkillVersion struct {
	ID          string `json:"id"`
	SkillID     string `json:"skill_id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	CreatedAt   int64  `json:"created_at"`
}

// SkillInlineContent is the JSON body that backs a skill version when
// the caller does not upload files. Matches the agentskills.io SKILL.md
// specification field-for-field.
type SkillInlineContent struct {
	Description   string            `json:"description"`
	Instructions  string            `json:"instructions"`
	License       string            `json:"license,omitempty"`
	Compatibility string            `json:"compatibility,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	AllowedTools  []string          `json:"allowed_tools,omitempty"`
}

// CreateVersionRequest is the JSON body for
// POST /skills/{name}/versions with application/json content type.
// When the skill does not yet exist, the service auto-creates it.
type CreateVersionRequest struct {
	InlineContent *SkillInlineContent `json:"inline_content,omitempty"`
	Default       bool                `json:"default,omitempty"`
}

// UpdateSkillRequest is the JSON body for POST /skills/{name}.
// Only the default version pointer is mutable on the Skill itself.
type UpdateSkillRequest struct {
	DefaultVersion string `json:"default_version"`
}

// DeleteSkillResponse is returned by DELETE /skills/{name}.
type DeleteSkillResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

// DeleteSkillVersionResponse is returned by DELETE /skills/{name}/versions/{version}.
type DeleteSkillVersionResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Deleted bool   `json:"deleted"`
}

// PagedResult is the standard Foundry paged-list envelope used by
// GET /skills and GET /skills/{name}/versions.
type PagedResult[T any] struct {
	Data    []T    `json:"data"`
	FirstID string `json:"first_id,omitempty"`
	LastID  string `json:"last_id,omitempty"`
	HasMore bool   `json:"has_more"`
}

// ListOptions configures a paged list request. Zero values use service defaults.
type ListOptions struct {
	Limit int
	Order string // "asc" | "desc"
}
