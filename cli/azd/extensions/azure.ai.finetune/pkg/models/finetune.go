// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

import (
	"fmt"
	"time"
)

// JobStatus represents the status of a fine-tuning job
type JobStatus string

// JobStatus constants define the possible states of a fine-tuning job
const (
	StatusPending   JobStatus = "pending"
	StatusQueued    JobStatus = "queued"
	StatusRunning   JobStatus = "running"
	StatusSucceeded JobStatus = "succeeded"
	StatusFailed    JobStatus = "failed"
	StatusCancelled JobStatus = "cancelled"
	StatusPaused    JobStatus = "paused"
)

// JobAction represents an action that can be performed on a fine-tuning job
type JobAction string

const (
    JobActionPause  JobAction = "pause"
    JobActionResume JobAction = "resume"
    JobActionCancel JobAction = "cancel"
)

// Represents the type of method used for fine-tuning
type MethodType string

// MethodType constants define the available fine-tuning methods
const (
	Supervised    MethodType = "supervised"
	DPO           MethodType = "dpo"
	Reinforcement MethodType = "reinforcement"
)

// Duration is a custom duration type that formats as "Xh XXm" in JSON/YAML output
type Duration time.Duration

// MarshalJSON implements json.Marshaler for Duration
// Returns the duration formatted as "Xh XXm" or "-" if zero
func (d Duration) MarshalJSON() ([]byte, error) {
	if d == 0 {
		return []byte(`"-"`), nil
	}

	h := int(time.Duration(d).Hours())
	m := int(time.Duration(d).Minutes()) % 60
	return []byte(fmt.Sprintf(`"%dh %02dm"`, h, m)), nil
}

// MarshalYAML implements yaml.Marshaler for Duration
// Returns the duration formatted as "Xh XXm" or "-" if zero
func (d Duration) MarshalYAML() (interface{}, error) {
	if d == 0 {
		return "-", nil
	}

	h := int(time.Duration(d).Hours())
	m := int(time.Duration(d).Minutes()) % 60
	return fmt.Sprintf("%dh %02dm", h, m), nil
}

// FineTuningJob represents a vendor-agnostic fine-tuning job
type FineTuningJob struct {
	// Core identification
	ID          string `json:"id" table:"ID"`
	VendorJobID string `json:"-" table:"-"` // Vendor-specific ID (e.g., OpenAI's ftjob-xxx)

	// Job details
	BaseModel      string    `json:"model" table:"MODEL"`
	Status         JobStatus `json:"status" table:"STATUS"`
	FineTunedModel string    `json:"-" table:"-"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at" table:"CREATED"`
	Duration    Duration   `json:"duration" table:"DURATION"`
	CompletedAt *time.Time `json:"-" table:"-"`

	// Files
	TrainingFileID   string `json:"-" table:"-"`
	ValidationFileID string `json:"-" table:"-"`

	// Metadata
	VendorMetadata map[string]interface{} `json:"-" table:"-"` // Store vendor-specific details
	ErrorDetails   *ErrorDetail           `json:"-" table:"-"`
}

// Hyperparameters represents fine-tuning hyperparameters
type Hyperparameters struct {
	BatchSize              int64   `json:"batch_size" yaml:"batch_size"`
	LearningRateMultiplier float64 `json:"learning_rate_multiplier" yaml:"learning_rate_multiplier"`
	NEpochs                int64   `json:"n_epochs" yaml:"n_epochs"`
	Beta                   float64 `json:"beta,omitempty" yaml:"beta,omitempty"`                             // For DPO
	ComputeMultiplier      float64 `json:"compute_multiplier,omitempty" yaml:"compute_multiplier,omitempty"` // For Reinforcement
	EvalInterval           int64   `json:"eval_interval,omitempty" yaml:"eval_interval,omitempty"`           // For Reinforcement
	EvalSamples            int64   `json:"eval_samples,omitempty" yaml:"eval_samples,omitempty"`             // For Reinforcement
	ReasoningEffort        string  `json:"reasoning_effort,omitempty" yaml:"reasoning_effort,omitempty"`     // For Reinforcement
}

// ListFineTuningJobsRequest represents a request to list fine-tuning jobs
type ListFineTuningJobsRequest struct {
	Limit int
	After string
}

// FineTuningJobDetail represents detailed information about a fine-tuning job
type FineTuningJobDetail struct {
	ID              string                 `json:"id" yaml:"id"`
	Status          JobStatus              `json:"status" yaml:"status"`
	Model           string                 `json:"model" yaml:"model"`
	FineTunedModel  string                 `json:"fine_tuned_model" yaml:"fine_tuned_model"`
	CreatedAt       time.Time              `json:"created_at" yaml:"created_at"`
	FinishedAt      *time.Time             `json:"finished_at,omitempty" yaml:"finished_at,omitempty"`
	EstimatedFinish *time.Time             `json:"estimated_finish,omitempty" yaml:"estimated_finish,omitempty"`
	Method          string                 `json:"training_type" yaml:"training_type"`
	TrainingFile    string                 `json:"training_file" yaml:"training_file"`
	ValidationFile  string                 `json:"validation_file,omitempty" yaml:"validation_file,omitempty"`
	Hyperparameters *Hyperparameters       `json:"hyperparameters" yaml:"hyperparameters"`
	VendorMetadata  map[string]interface{} `json:"-" yaml:"-"`
	Seed            int64                  `json:"-" yaml:"-"`
}

// JobEvent represents an event associated with a fine-tuning job
type JobEvent struct {
	ID        string
	CreatedAt time.Time
	Level     string
	Message   string
	Data      interface{}
	Type      string
}

// JobEventsList represents a paginated list of job events
type JobEventsList struct {
	Data    []JobEvent
	HasMore bool
}

// JobCheckpoint represents a checkpoint of a fine-tuning job
type JobCheckpoint struct {
	ID                       string
	CreatedAt                time.Time
	FineTunedModelCheckpoint string
	Metrics                  *CheckpointMetrics
	FineTuningJobID          string
	StepNumber               int64
}

// JobCheckpointsList represents a list of job checkpoints
type JobCheckpointsList struct {
	Data    []JobCheckpoint
	HasMore bool
}

// CheckpointMetrics represents metrics for a checkpoint
type CheckpointMetrics struct {
	FullValidLoss              float64
	FullValidMeanTokenAccuracy float64
}

// CreateFineTuningRequest represents a request to create a fine-tuning job
type CreateFineTuningRequest struct {
	// Required: The name of the model to fine-tune
	BaseModel string `yaml:"model"`

	// Required: Path to training file
	// Format: "file-id" or "local:/path/to/file.jsonl"
	TrainingFile string `yaml:"training_file"`

	// Optional: Path to validation file
	ValidationFile *string `yaml:"validation_file,omitempty"`

	// Optional: Suffix for the fine-tuned model name (up to 64 characters)
	// Example: "custom-model-name" produces "ft:gpt-4o-mini:openai:custom-model-name:7p4lURel"
	Suffix *string `yaml:"suffix,omitempty"`

	// Optional: Random seed for reproducibility
	Seed *int64 `yaml:"seed,omitempty"`

	// Optional: Custom metadata for the fine-tuning job
	// Max 16 key-value pairs, keys max 64 chars, values max 512 chars
	Metadata map[string]string `yaml:"metadata,omitempty"`

	// Optional: Fine-tuning method configuration (supervised, dpo, or reinforcement)
	Method MethodConfig `yaml:"method,omitempty"`

	// Optional: Integrations to enable (e.g., wandb for Weights & Biases)
	Integrations []Integration `yaml:"integrations,omitempty"`

	// Optional: Additional request body fields not covered by standard config
	ExtraBody map[string]interface{} `yaml:"extra_body,omitempty"`
}

// MethodConfig represents fine-tuning method configuration
type MethodConfig struct {
	// Type of fine-tuning method: "supervised", "dpo", or "reinforcement"
	Type string `yaml:"type"`

	// Supervised fine-tuning configuration
	Supervised *SupervisedConfig `yaml:"supervised,omitempty"`

	// Direct Preference Optimization (DPO) configuration
	DPO *DPOConfig `yaml:"dpo,omitempty"`

	// Reinforcement learning fine-tuning configuration
	Reinforcement *ReinforcementConfig `yaml:"reinforcement,omitempty"`
}

// SupervisedConfig represents supervised fine-tuning method configuration
// Suitable for standard supervised learning tasks
type SupervisedConfig struct {
	Hyperparameters HyperparametersConfig `yaml:"hyperparameters,omitempty"`
}

// DPOConfig represents Direct Preference Optimization (DPO) configuration
// DPO is used for preference-based fine-tuning
type DPOConfig struct {
	Hyperparameters HyperparametersConfig `yaml:"hyperparameters,omitempty"`
}

// ReinforcementConfig represents reinforcement learning fine-tuning configuration
// Suitable for reasoning models that benefit from reinforcement learning
type ReinforcementConfig struct {
	// Grader configuration for reinforcement learning (evaluates model outputs)
	Grader map[string]interface{} `yaml:"grader,omitempty"`

	// Hyperparameters specific to reinforcement learning
	Hyperparameters HyperparametersConfig `yaml:"hyperparameters,omitempty"`
}

// HyperparametersConfig represents hyperparameter configuration
// Values can be integers, floats, or "auto" for automatic configuration
type HyperparametersConfig struct {
	// Number of training epochs
	// Can be: integer (1-10), "auto"
	Epochs interface{} `yaml:"epochs,omitempty"`

	// Batch size for training
	// Can be: integer (1, 8, 16, 32, 64, 128), "auto"
	BatchSize interface{} `yaml:"batch_size,omitempty"`

	// Learning rate multiplier
	// Can be: float (0.1-2.0), "auto"
	LearningRateMultiplier interface{} `yaml:"learning_rate_multiplier,omitempty"`

	// Weight for prompt loss in supervised learning (0.0-1.0)
	PromptLossWeight *float64 `yaml:"prompt_loss_weight,omitempty"`

	// Beta parameter for DPO (temperature-like parameter)
	// Can be: float, "auto"
	Beta interface{} `yaml:"beta,omitempty"`

	// Compute multiplier for reinforcement learning
	// Multiplier on amount of compute used for exploring search space during training
	// Can be: float, "auto"
	ComputeMultiplier interface{} `yaml:"compute_multiplier,omitempty"`

	// Reasoning effort level for reinforcement learning with reasoning models
	// Options: "low", "medium", "high"
	ReasoningEffort string `yaml:"reasoning_effort,omitempty"`

	// Evaluation interval for reinforcement learning
	// Number of training steps between evaluation runs
	// Can be: integer, "auto"
	EvalInterval interface{} `yaml:"eval_interval,omitempty"`

	// Evaluation samples for reinforcement learning
	// Number of evaluation samples to generate per training step
	// Can be: integer, "auto"
	EvalSamples interface{} `yaml:"eval_samples,omitempty"`
}

// Integration represents integration configuration (e.g., Weights & Biases)
type Integration struct {
	// Type of integration: "wandb" (Weights & Biases), etc.
	Type string `yaml:"type"`

	// Integration-specific configuration (API keys, project names, etc.)
	Config map[string]interface{} `yaml:"config,omitempty"`
}

// Validate checks if the configuration is valid
func (c CreateFineTuningRequest) Validate() error {
	// Validate required fields
	if c.BaseModel == "" {
		return fmt.Errorf("model is required")
	}

	if c.TrainingFile == "" {
		return fmt.Errorf("training_file is required")
	}

	// Validate method if provided
	if c.Method.Type != "" {
		if c.Method.Type != string(Supervised) && c.Method.Type != string(DPO) && c.Method.Type != string(Reinforcement) {
			return fmt.Errorf("invalid method type: %s (must be 'supervised', 'dpo', or 'reinforcement')", c.Method.Type)
		}

		// Validate method-specific configuration
		switch c.Method.Type {
		case string(Supervised):
			if c.Method.Supervised == nil {
				return fmt.Errorf("supervised method requires 'supervised' configuration block")
			}
		case string(DPO):
			if c.Method.DPO == nil {
				return fmt.Errorf("dpo method requires 'dpo' configuration block")
			}
		case string(Reinforcement):
			if c.Method.Reinforcement == nil {
				return fmt.Errorf("reinforcement method requires 'reinforcement' configuration block")
			}
		}
	}

	// Validate integrations if provided
	if len(c.Integrations) > 0 {
		for _, integration := range c.Integrations {
			if integration.Type == "" {
				return fmt.Errorf("integration type is required if integrations are specified")
			}
			if integration.Config == nil {
				return fmt.Errorf("integration of type '%s' requires 'config' block", integration.Type)
			}
		}
	}

	// Validate suffix length if provided
	if c.Suffix != nil && len(*c.Suffix) > 64 {
		return fmt.Errorf("suffix exceeds maximum length of 64 characters: %d", len(*c.Suffix))
	}

	// Validate metadata constraints
	if c.Metadata != nil {
		if len(c.Metadata) > 16 {
			return fmt.Errorf("metadata exceeds maximum of 16 key-value pairs: %d", len(c.Metadata))
		}
		for k, v := range c.Metadata {
			if len(k) > 64 {
				return fmt.Errorf("metadata key exceeds maximum length of 64 characters: %s", k)
			}
			if len(v) > 512 {
				return fmt.Errorf("metadata value exceeds maximum length of 512 characters for key: %s", k)
			}
		}
	}

	return nil
}
