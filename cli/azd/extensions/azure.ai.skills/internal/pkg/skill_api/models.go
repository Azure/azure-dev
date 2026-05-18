// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package skill_api provides a typed REST client for the Foundry Skills
// data-plane surface, plus helpers for parsing SKILL.md files and safely
// extracting downloaded skill packages.
package skill_api

// Skill is the metadata representation of a Foundry skill. JSON fields are
// camelCase for the published output of the CLI; the wire format is
// snake_case and is translated via skillWire.
type Skill struct {
	SkillID     string            `json:"skillId,omitempty"`
	Name        string            `json:"name"`
	HasBlob     bool              `json:"hasBlob"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

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

// CreateRequest is the JSON body for POST /skills. The CLI populates Name
// from the positional argument and never trusts a value from front matter.
type CreateRequest struct {
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// UpdateRequest is the merged JSON body for POST /skills/{name}.
type UpdateRequest struct {
	Description  string            `json:"description,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// DeleteResponse is the JSON body returned by DELETE /skills/{name}.
type DeleteResponse struct {
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

// PagedSkills is one page of the GET /skills response.
type PagedSkills struct {
	Data    []Skill `json:"data"`
	FirstID string  `json:"firstId,omitempty"`
	LastID  string  `json:"lastId,omitempty"`
	HasMore bool    `json:"hasMore"`
}

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

// ListOptions configures a GET /skills request. Zero values use service defaults.
type ListOptions struct {
	Top     int
	OrderBy string
}
