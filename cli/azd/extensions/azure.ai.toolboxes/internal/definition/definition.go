// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package definition owns the source-controlled toolbox definition model.
package definition

import (
	"errors"
	"fmt"
	"strings"
)

const DefaultPath = "toolbox.yaml"

var (
	ErrDuplicateConnection = errors.New("connection is already referenced")
	ErrDuplicateSkill      = errors.New("skill is already referenced")
)

// Definition is the deploy-ready representation of a Foundry toolbox.
type Definition struct {
	Name        string                `json:"name,omitempty" yaml:"name,omitempty"`
	Description string                `json:"description,omitempty" yaml:"description,omitempty"`
	Connections []ConnectionReference `json:"connections,omitempty" yaml:"connections,omitempty"`
	Skills      []SkillReference      `json:"skills,omitempty" yaml:"skills,omitempty"`
	Tools       []map[string]any      `json:"tools,omitempty" yaml:"tools,omitempty"`
	Policies    *Policies             `json:"policies,omitempty" yaml:"policies,omitempty"`
	Metadata    map[string]string     `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// ConnectionReference identifies a project connection used by a toolbox tool.
type ConnectionReference struct {
	Name         string `json:"name" yaml:"name"`
	Index        string `json:"index,omitempty" yaml:"index,omitempty"`
	InstanceName string `json:"instance_name,omitempty" yaml:"instance_name,omitempty"`
}

// SkillReference identifies a project skill and optionally pins its version.
type SkillReference struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
}

// Policies contains per-version toolbox governance settings.
type Policies struct {
	RaiConfig *RaiConfig `json:"rai_config,omitempty" yaml:"rai_config,omitempty"`
}

// RaiConfig identifies the Responsible AI policy applied to a toolbox version.
type RaiConfig struct {
	RaiPolicyName string `json:"rai_policy_name,omitempty" yaml:"rai_policy_name,omitempty"`
	Name          string `json:"name,omitempty" yaml:"name,omitempty"`
}

// ResolvedPolicyName returns the wire-shaped policy name, accepting name as a
// compatibility alias.
func (r *RaiConfig) ResolvedPolicyName() string {
	if r == nil {
		return ""
	}
	if name := strings.TrimSpace(r.RaiPolicyName); name != "" {
		return name
	}
	return strings.TrimSpace(r.Name)
}

// AddConnection adds a connection reference without contacting Foundry.
func (d *Definition) AddConnection(ref ConnectionReference) error {
	ref.Name = strings.TrimSpace(ref.Name)
	ref.Index = strings.TrimSpace(ref.Index)
	ref.InstanceName = strings.TrimSpace(ref.InstanceName)
	if ref.Name == "" {
		return fmt.Errorf("connection name must not be empty")
	}
	for _, existing := range d.Connections {
		if strings.TrimSpace(existing.Name) == ref.Name {
			return fmt.Errorf("%w: %q", ErrDuplicateConnection, ref.Name)
		}
	}
	d.Connections = append(d.Connections, ref)
	return nil
}

// AddSkill adds a skill reference without contacting Foundry.
func (d *Definition) AddSkill(ref SkillReference) error {
	ref.Name = strings.TrimSpace(ref.Name)
	ref.Version = strings.TrimSpace(ref.Version)
	if ref.Name == "" {
		return fmt.Errorf("skill name must not be empty")
	}
	for _, existing := range d.Skills {
		if strings.TrimSpace(existing.Name) == ref.Name {
			return fmt.Errorf("%w: %q", ErrDuplicateSkill, ref.Name)
		}
	}
	d.Skills = append(d.Skills, ref)
	return nil
}
