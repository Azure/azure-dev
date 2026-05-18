// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package skill_api provides a typed REST client for the Foundry Skills
// data-plane surface, plus helpers for parsing SKILL.md files and safely
// extracting downloaded ZIP skill packages.
package skill_api

// Skill is the metadata representation of a Foundry skill returned by
// the Skills data-plane surface. Fields are camelCase in JSON to match the
// published JSON contract for the CLI; the wire format from the service is
// snake_case and is translated by the wire-to-public conversions below.
type Skill struct {
	// SkillID is the unique service-assigned identifier.
	SkillID string `json:"skillId,omitempty"`
	// Name is the unique skill name (validated client-side against the
	// alphanumeric-with-hyphens pattern; final decision lives in the service).
	Name string `json:"name"`
	// HasBlob reports whether the skill was created from a ZIP package.
	// Determines whether `download` returns useful content.
	HasBlob bool `json:"hasBlob"`
	// Description is a human-readable summary; optional.
	Description string `json:"description,omitempty"`
	// Metadata is the freeform string-to-string map the service stores
	// alongside the skill. May be nil.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// skillWire mirrors Skill but uses the snake_case wire field names that the
// Foundry Skills surface returns. Decoding into Skill goes through this struct
// so the public JSON contract for the CLI stays camelCase regardless of how
// the service evolves.
type skillWire struct {
	SkillID     string            `json:"skill_id,omitempty"`
	Name        string            `json:"name"`
	HasBlob     bool              `json:"has_blob,omitempty"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func (w skillWire) toSkill() Skill {
	return Skill{
		SkillID:     w.SkillID,
		Name:        w.Name,
		HasBlob:     w.HasBlob,
		Description: w.Description,
		Metadata:    w.Metadata,
	}
}

// CreateRequest is the inline JSON body for `POST /skills`. Either both of
// Description and Instructions are set (inline mode) or they come from a
// parsed SKILL.md file. The CLI populates Name from the positional argument
// and never trusts the front-matter value.
type CreateRequest struct {
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// UpdateRequest is the merged JSON body for `POST /skills/{name}`. Only
// non-empty fields are sent; the action layer performs the GET-merge-POST.
type UpdateRequest struct {
	Description  string            `json:"description,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// DeleteResponse is the JSON body returned by `DELETE /skills/{name}`.
type DeleteResponse struct {
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

// PagedSkills is one page of the `GET /skills` response.
type PagedSkills struct {
	Data    []Skill `json:"data"`
	FirstID string  `json:"firstId,omitempty"`
	LastID  string  `json:"lastId,omitempty"`
	HasMore bool    `json:"hasMore"`
}

// pagedSkillsWire is the snake_case wire form of PagedSkills.
type pagedSkillsWire struct {
	Data    []skillWire `json:"data"`
	FirstID string      `json:"first_id,omitempty"`
	LastID  string      `json:"last_id,omitempty"`
	HasMore bool        `json:"has_more"`
}

func (w pagedSkillsWire) toPagedSkills() PagedSkills {
	out := PagedSkills{
		FirstID: w.FirstID,
		LastID:  w.LastID,
		HasMore: w.HasMore,
	}
	if len(w.Data) > 0 {
		out.Data = make([]Skill, 0, len(w.Data))
		for _, item := range w.Data {
			out.Data = append(out.Data, item.toSkill())
		}
	}
	return out
}

// ListOptions configures a `GET /skills` request. Zero values mean "let the
// service apply its defaults".
type ListOptions struct {
	// Top is the per-page item limit. The service caps this at 100.
	Top int
	// OrderBy is forwarded to the `order` query parameter
	// (typically `asc` or `desc`).
	OrderBy string
}
