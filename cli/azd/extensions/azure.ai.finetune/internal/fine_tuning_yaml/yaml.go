// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package fine_tuning_yaml

// MethodType represents the type of method used for fine-tuning
type MethodType string

const (
	Supervised    MethodType = "supervised"
	DPO           MethodType = "dpo"
	Reinforcement MethodType = "reinforcement"
)

// FineTuningConfig represents the YAML configuration structure for fine-tuning jobs
// This schema aligns with OpenAI Fine-Tuning API requirements
type FineTuningConfig struct {
	// Required: The name of the model to fine-tune
	// Supported models: gpt-4o-mini, gpt-4o, gpt-4-turbo, etc.
	Model string `yaml:"model"`

	// Required: Path to training file
	// Format: "file-id" or "local:/path/to/file.jsonl"
	TrainingFile string `yaml:"training_file"`

	// Optional: Path to validation file
	ValidationFile string `yaml:"validation_file,omitempty"`

	// Optional: Fine-tuning method configuration (supervised, dpo, or reinforcement)
	Method MethodConfig `yaml:"method,omitempty"`

	// Optional: Suffix for the fine-tuned model name (up to 64 characters)
	// Example: "custom-model-name" produces "ft:gpt-4o-mini:openai:custom-model-name:7p4lURel"
	Suffix *string `yaml:"suffix,omitempty"`

	// Optional: Random seed for reproducibility
	Seed *int64 `yaml:"seed,omitempty"`

	// Optional: Custom metadata for the fine-tuning job
	// Max 16 key-value pairs, keys max 64 chars, values max 512 chars
	Metadata map[string]string `yaml:"metadata,omitempty"`

	// Optional: Integrations to enable (e.g., wandb for Weights & Biases)
	Integrations []IntegrationConfig `yaml:"integrations,omitempty"`

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
	Grader GraderConfig `yaml:"grader,omitempty"`

	// Hyperparameters specific to reinforcement learning
	Hyperparameters HyperparametersConfig `yaml:"hyperparameters,omitempty"`
}

// GraderConfig represents grader configuration for reinforcement learning
// The grader evaluates and scores fine-tuning outputs
// Supports one of: StringCheckGrader, TextSimilarityGrader, PythonGrader, ScoreModelGrader, or MultiGrader
type GraderConfig struct {
	// Type of grader: "string_check", "text_similarity", "python", "score_model", or "multi"
	Type string `yaml:"type,omitempty"`

	// StringCheckGrader: Performs string comparison between input and reference
	StringCheck *StringCheckGraderConfig `yaml:"string_check,omitempty"`

	// TextSimilarityGrader: Grades based on text similarity metrics
	TextSimilarity *TextSimilarityGraderConfig `yaml:"text_similarity,omitempty"`

	// PythonGrader: Runs a Python script for evaluation
	Python *PythonGraderConfig `yaml:"python,omitempty"`

	// ScoreModelGrader: Uses a model to assign scores
	ScoreModel *ScoreModelGraderConfig `yaml:"score_model,omitempty"`

	// MultiGrader: Combines multiple graders for composite scoring
	Multi *MultiGraderConfig `yaml:"multi,omitempty"`
}

// StringCheckGraderConfig performs string comparison evaluation
type StringCheckGraderConfig struct {
	// Type: always "string_check"
	Type string `yaml:"type"`

	// The input field to check (reference to {{ item.XXX }} in training data)
	Input string `yaml:"input"`

	// Name of the grader
	Name string `yaml:"name"`

	// Operation to perform: "eq" (equals), "contains", "regex"
	Operation string `yaml:"operation"`

	// Reference value to compare against (can use {{ item.XXX }} template)
	Reference string `yaml:"reference"`
}

// TextSimilarityGraderConfig grades based on text similarity
type TextSimilarityGraderConfig struct {
	// Type: always "text_similarity"
	Type string `yaml:"type"`

	// Name of the grader
	Name string `yaml:"name"`

	// The text being graded (input field to evaluate)
	Input string `yaml:"input"`

	// Reference text to compare similarity against
	Reference string `yaml:"reference"`

	// Evaluation metric to use
	// Options: "cosine", "fuzzy_match", "bleu", "gleu", "meteor",
	// "rouge_1", "rouge_2", "rouge_3", "rouge_4", "rouge_5", "rouge_l"
	EvaluationMetric string `yaml:"evaluation_metric"`
}

// PythonGraderConfig runs Python code for evaluation
type PythonGraderConfig struct {
	// Type: always "python"
	Type string `yaml:"type"`

	// Name of the grader
	Name string `yaml:"name"`

	// Source code of the Python script
	// Must define a function that evaluates and returns a score
	Source string `yaml:"source"`

	// Optional: Docker image tag to use for the Python script execution
	ImageTag string `yaml:"image_tag,omitempty"`
}

// ScoreModelGraderConfig uses a model for scoring
type ScoreModelGraderConfig struct {
	// Type: always "score_model"
	Type string `yaml:"type"`

	// Name of the grader
	Name string `yaml:"name"`

	// The input messages evaluated by the grader
	// Supports text, output text, input image, and input audio content blocks
	// May include template strings (e.g., {{ item.output }})
	Input []MessageInputConfig `yaml:"input"`

	// Model to use for scoring (e.g., "gpt-4", "gpt-4o")
	Model string `yaml:"model"`

	// Optional: The range of the score (e.g., [0, 1])
	// Defaults to [0, 1]
	Range []float64 `yaml:"range,omitempty"`

	// Optional: Sampling parameters for the model
	SamplingParams *SamplingParamsConfig `yaml:"sampling_params,omitempty"`
}

// MessageInputConfig represents a message input for score model grader
type MessageInputConfig struct {
	// Role of the message: "user", "assistant", "system", or "developer"
	Role string `yaml:"role"`

	// Optional: Type of the message input. Always "message"
	Type string `yaml:"type,omitempty"`

	// Content blocks in the message
	// Can contain one or more content items: input text, output text, input image, or input audio
	// Can include template strings (e.g., {{ item.output }})
	Content []ContentItem `yaml:"content"`
}

// ContentItem represents a single content item in a message
// Can be one of: InputTextContent, OutputTextContent, InputImageContent, or InputAudioContent
type ContentItem struct {
	// Type of content: "text" or "image" or "audio"
	Type string `yaml:"type,omitempty"`

	// For text content (input or output): the text content
	// Can include template strings
	Text string `yaml:"text,omitempty"`

	// For image content: URL or base64-encoded image data
	Image string `yaml:"image,omitempty"`

	// For audio content: URL or base64-encoded audio data
	AudioURL string `yaml:"audio_url,omitempty"`

	// For audio content (optional): audio format/codec
	Format string `yaml:"format,omitempty"`
}

// InputTextContent represents input text content
type InputTextContent struct {
	Type string `yaml:"type"` // "text"
	Text string `yaml:"text"` // Can include template strings like {{ item.input }}
}

// OutputTextContent represents output text content
type OutputTextContent struct {
	Type string `yaml:"type"` // "text"
	Text string `yaml:"text"` // Can include template strings like {{ item.output }}
}

// InputImageContent represents input image content
type InputImageContent struct {
	Type  string `yaml:"type"`  // "image"
	Image string `yaml:"image"` // URL or base64-encoded image data
}

// InputAudioContent represents input audio content
type InputAudioContent struct {
	Type     string `yaml:"type"`             // "audio"
	AudioURL string `yaml:"audio_url"`        // URL or base64-encoded audio data
	Format   string `yaml:"format,omitempty"` // Optional: audio format/codec
}

// SamplingParamsConfig represents sampling parameters for score model grader
type SamplingParamsConfig struct {
	// Optional: Maximum number of tokens the grader model may generate
	MaxCompletionsTokens *int64 `yaml:"max_completions_tokens,omitempty"`

	// Optional: Reasoning effort level ("none", "minimal", "low", "medium", "high", "xhigh")
	// Defaults to "medium"
	// Note: gpt-5.1 defaults to "none" and only supports "none", "low", "medium", "high"
	// gpt-5-pro defaults to and only supports "high"
	ReasoningEffort string `yaml:"reasoning_effort,omitempty"`
}

// MultiGraderConfig combines multiple graders
type MultiGraderConfig struct {
	// Type: always "multi"
	Type string `yaml:"type"`

	// List of graders to combine
	Graders []map[string]interface{} `yaml:"graders"`

	// How to combine scores: "average", "weighted", "min", "max"
	Aggregation string `yaml:"aggregation,omitempty"`

	// Weights for each grader (for weighted aggregation)
	Weights []float64 `yaml:"weights,omitempty"`
}

// HyperparametersConfig represents hyperparameter configuration
// Values can be integers, floats, or "auto" for automatic configuration
type HyperparametersConfig struct {
	// Number of training epochs
	// Can be: integer (1-10), "auto" (OpenAI determines optimal value)
	Epochs interface{} `yaml:"epochs,omitempty"`

	// Batch size for training
	// Can be: integer (1, 8, 16, 32, 64, 128), "auto" (OpenAI determines optimal value)
	BatchSize interface{} `yaml:"batch_size,omitempty"`

	// Learning rate multiplier
	// Can be: float (0.1-2.0), "auto" (OpenAI determines optimal value)
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

// IntegrationConfig represents integration configuration (e.g., Weights & Biases)
type IntegrationConfig struct {
	// Type of integration: "wandb" (Weights & Biases), etc.
	Type string `yaml:"type"`

	// Integration-specific configuration (API keys, project names, etc.)
	Config map[string]interface{} `yaml:"config,omitempty"`
}
