// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package rollouts reads completed rollout artifacts independently of the dashboard.
package rollouts

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Response describes the fields used by the CLI; Raw retains the complete service response.
type Response struct {
	RolloutID string          `json:"rollout_id"`
	Rollout   json.RawMessage `json:"rollout"`
	Reward    float64         `json:"reward"`
	Success   *bool           `json:"success,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Episode   *Episode        `json:"episode,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

// Episode contains Gym/OpenEnv annotations, not a task-level success verdict.
type Episode struct {
	Kind              string `json:"kind"`
	TerminationReason string `json:"termination_reason"`
	Steps             []Step `json:"steps"`
	Ungraded          *bool  `json:"ungraded,omitempty"`
}

// Step links an environment action's reward to the model turn that produced it.
type Step struct {
	CaptureNodeID string  `json:"capture_node_id"`
	Reward        float64 `json:"reward"`
	EpisodeDone   bool    `json:"episode_done"`
}

// UnmarshalJSON preserves unknown fields and numeric precision for subsequent persistence.
func (r *Response) UnmarshalJSON(data []byte) error {
	type fields Response
	var decoded fields
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("decode rollout response: %w", err)
	}
	*r = Response(decoded)
	r.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// ValidateID accepts the service's canonical, lowercase, 32-character GUID format.
func ValidateID(id string) error {
	if len(id) != 32 || id != strings.ToLower(id) {
		return fmt.Errorf("rollout ID must be 32 lowercase hexadecimal characters")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return fmt.Errorf("rollout ID must be 32 lowercase hexadecimal characters")
	}
	return nil
}

func decodeResponse(data json.RawMessage) (*Response, error) {
	var response Response
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	if err := ValidateID(response.RolloutID); err != nil {
		return nil, err
	}
	var required struct {
		Reward *float64 `json:"reward"`
	}
	if err := json.Unmarshal(data, &required); err != nil {
		return nil, err
	}
	if required.Reward == nil {
		return nil, fmt.Errorf("rollout response is missing reward")
	}
	return &response, nil
}
