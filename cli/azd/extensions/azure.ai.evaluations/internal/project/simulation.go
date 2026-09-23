// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import "fmt"

// Simulation declares that an eval creates its conversations rather than
// scoring ones it was given.
//
// Its presence is what selects the simulation run type. There is no separate
// mode field because existing evals have no such field and the block is
// unambiguous on its own: a dataset of scenario seeds is only useful to an eval
// that simulates, and an eval that simulates has nothing else to read.
type Simulation struct {
	// Model is the deployment the simulated user speaks with. It is not the
	// judge model an evaluator initializes, and not the model that generated
	// the seeds -- three separate choices that happen to be models.
	Model string `yaml:"model,omitempty"             json:"model,omitempty"`

	// NumConversations is how many conversations to create per scenario.
	NumConversations int `yaml:"num_conversations,omitempty" json:"num_conversations,omitempty"`

	// MaxTurns bounds each conversation. Omitted leaves the service's own
	// default rather than imposing one here, which would silently truncate
	// conversations a caller never asked to bound.
	MaxTurns int `yaml:"max_turns,omitempty"         json:"max_turns,omitempty"`
}

// Bounds on the simulation parameters, from ConversationGenerationParams in
// the REST contract. They are checked locally so an out-of-range value is
// refused before anything is created rather than after a run is billed.
const (
	MinNumConversations = 1
	MaxNumConversations = 5
	MinSimulationTurns  = 1
	MaxSimulationTurns  = 20

	// DefaultNumConversations is what an unstated count means. MaxTurns has no
	// local default: omitted means the service decides.
	DefaultNumConversations = 1
)

// UnmarshalYAML refuses an explicitly written zero.
//
// Both counts use 0 as the "unstated" sentinel, which the rest of this config
// model does too, so Validate cannot tell `num_conversations: 0` from a key
// that was never there -- and the zero was quietly replaced with the default
// while the schema declares a minimum of 1. The editor refused it and the CLI
// accepted it, which is the disagreement worth closing.
//
// Presence is read here, where it still exists, rather than by making the
// fields pointers: 0-means-unset is the convention every other optional number
// in this package follows, and one struct disagreeing is its own trap.
// The callback works with both YAML packages and preserves the calling decoder's
// strict-field checks.
func (s *Simulation) UnmarshalYAML(unmarshal func(any) error) error {
	type simulationYAML Simulation
	var decoded simulationYAML
	if err := unmarshal(&decoded); err != nil {
		return err
	}
	var declared map[string]any
	if err := unmarshal(&declared); err != nil {
		return err
	}

	for _, stated := range []struct {
		key   string
		value int
		min   int
	}{
		{"num_conversations", decoded.NumConversations, MinNumConversations},
		{"max_turns", decoded.MaxTurns, MinSimulationTurns},
	} {
		if _, present := declared[stated.key]; present && stated.value == 0 {
			return fmt.Errorf(
				"simulation.%s is 0; omit it for the default, or give it at least %d",
				stated.key, stated.min)
		}
	}

	*s = Simulation(decoded)
	return nil
}

// Validate refuses a simulation block that cannot produce a run.
//
// Each message names the field, what it was given, and what it accepts,
// because the value came from a file the reader has open and can correct.
func (s *Simulation) Validate() error {
	if s == nil {
		return nil
	}

	if s.Model == "" {
		return fmt.Errorf("simulation.model is required: it names the deployment the simulated user speaks with")
	}

	if s.NumConversations != 0 &&
		(s.NumConversations < MinNumConversations || s.NumConversations > MaxNumConversations) {
		return fmt.Errorf(
			"simulation.num_conversations is %d; it accepts %d to %d",
			s.NumConversations, MinNumConversations, MaxNumConversations)
	}

	if s.MaxTurns != 0 &&
		(s.MaxTurns < MinSimulationTurns || s.MaxTurns > MaxSimulationTurns) {
		return fmt.Errorf(
			"simulation.max_turns is %d; it accepts %d to %d",
			s.MaxTurns, MinSimulationTurns, MaxSimulationTurns)
	}

	return nil
}

// Conversations is the count to request, applying the default for an unstated
// one so callers do not each decide what "not set" means.
func (s *Simulation) Conversations() int {
	if s == nil || s.NumConversations == 0 {
		return DefaultNumConversations
	}
	return s.NumConversations
}
