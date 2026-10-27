// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Diagnostic struct {
	Code    string
	Message string
	Target  string
	Details []Diagnostic
}

type Usage struct {
	DocumentPagesMinimal            *int64           `json:"documentPagesMinimal,omitempty"`
	DocumentPagesBasic              *int64           `json:"documentPagesBasic,omitempty"`
	DocumentPagesStandard           *int64           `json:"documentPagesStandard,omitempty"`
	DocumentPagesMinimalInline      *int64           `json:"documentPagesMinimalInline,omitempty"`
	DocumentPagesBasicInline        *int64           `json:"documentPagesBasicInline,omitempty"`
	DocumentPagesStandardInline     *int64           `json:"documentPagesStandardInline,omitempty"`
	AudioHours                      *float64         `json:"audioHours,omitempty"`
	VideoHours                      *float64         `json:"videoHours,omitempty"`
	ContextualizationTokens         *int64           `json:"contextualizationTokens,omitempty"`
	AdvancedContextualizationTokens *int64           `json:"advancedContextualizationTokens,omitempty"`
	Tokens                          map[string]int64 `json:"tokens,omitempty"`
}

// JSON retains complete service output, including dynamic schemas and fields,
// without making commands depend on transport or client models.
type Analyzer struct {
	ID             string
	Description    string
	Status         string
	CreatedAt      time.Time
	LastModifiedAt time.Time
	Warnings       []Diagnostic
	JSON           json.RawMessage
}

type AnalyzerResponse struct {
	Analyzer Analyzer
	Usage    *Usage
}

type CreateAnalyzerRequest struct {
	AnalyzerID string
	Definition json.RawMessage
}

type GrantCopyAuthorizationRequest struct {
	AnalyzerID            string
	TargetAzureResourceID string
	TargetRegion          string
}

// CopyAuthorization is a server-side grant, not a token to forward in CopyAnalyzer.
// It must not be printed or persisted by the command layer.
type CopyAuthorization struct {
	Source                string
	TargetAzureResourceID string
	ExpiresAt             time.Time
}

// CopyAnalyzer is called on the destination resource's client.
type CopyAnalyzerRequest struct {
	SourceAnalyzerID      string
	DestinationAnalyzerID string
	SourceAzureResourceID string
	SourceRegion          string
}

type Defaults struct {
	ModelDeployments map[string]string
	JSON             json.RawMessage
}

type UpdateDefaultsRequest struct {
	ModelDeployments map[string]string
}

type AnalysisMode string

const (
	AnalysisModeAsync  AnalysisMode = "async"
	AnalysisModeInline AnalysisMode = "inline"
)

// Input contains exactly one remote URL or caller-owned binary reader.
type Input struct {
	URL         string
	Body        io.ReadSeeker
	ContentType string
}

type AnalyzeRequest struct {
	AnalyzerID string
	Input      Input
	Mode       AnalysisMode
}

type Content struct {
	Kind     string
	Markdown string
	Fields   map[string]json.RawMessage
	JSON     json.RawMessage
}

type AnalysisResult struct {
	AnalyzerID string
	APIVersion string
	CreatedAt  time.Time
	Contents   []Content
	Warnings   []Diagnostic
	Infos      []Diagnostic
	JSON       json.RawMessage
}

type AnalyzeResponse struct {
	Result AnalysisResult
	Usage  *Usage
	// JSON retains the complete terminal response envelope for full-result output.
	// Result.JSON retains only the nested analysis result.
	JSON json.RawMessage
}

var analyzerIDPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

func ValidateAnalyzerID(id string) error {
	if !analyzerIDPattern.MatchString(id) {
		return validationError("invalid_analyzer_id",
			"Analyzer IDs must contain 1-64 ASCII letters, digits, dots, underscores, or hyphens.")
	}
	return nil
}

func (r CreateAnalyzerRequest) Validate() error {
	if err := ValidateAnalyzerID(r.AnalyzerID); err != nil {
		return err
	}
	var definition map[string]json.RawMessage
	if err := json.Unmarshal(r.Definition, &definition); err != nil || definition == nil {
		return validationError("invalid_analyzer_definition", "The analyzer definition must be a JSON object.")
	}
	return nil
}

func (r GrantCopyAuthorizationRequest) Validate() error {
	if err := ValidateAnalyzerID(r.AnalyzerID); err != nil {
		return err
	}
	if strings.TrimSpace(r.TargetAzureResourceID) == "" {
		return validationError("missing_copy_target", "Copy authorization requires a target Azure resource ID.")
	}
	return nil
}

func (r CopyAnalyzerRequest) Validate() error {
	if err := ValidateAnalyzerID(r.SourceAnalyzerID); err != nil {
		return err
	}
	if err := ValidateAnalyzerID(r.DestinationAnalyzerID); err != nil {
		return err
	}
	if r.SourceAzureResourceID == "" && r.SourceRegion != "" {
		return validationError("missing_copy_source", "A source region requires a source Azure resource ID.")
	}
	if r.SourceAzureResourceID == "" && r.SourceAnalyzerID == r.DestinationAnalyzerID {
		return validationError("identical_copy_target", "Same-resource copy requires a different destination analyzer ID.")
	}
	return nil
}

func (r UpdateDefaultsRequest) Validate() error {
	if r.ModelDeployments == nil {
		return validationError("missing_model_deployments", "Specify a model deployment mapping.")
	}
	for model, deployment := range r.ModelDeployments {
		if strings.TrimSpace(model) == "" || strings.TrimSpace(deployment) == "" {
			return validationError("invalid_model_deployment", "Model names and deployment names must be nonempty.")
		}
	}
	return nil
}

func (r AnalyzeRequest) Validate() error {
	if err := ValidateAnalyzerID(r.AnalyzerID); err != nil {
		return err
	}
	if r.Mode != AnalysisModeAsync && r.Mode != AnalysisModeInline {
		return validationError("invalid_analysis_mode", "Select async or inline analysis explicitly.")
	}
	if (r.Input.URL == "") == (r.Input.Body == nil) {
		return validationError("invalid_analysis_input", "Specify exactly one input URL or binary reader.")
	}
	if r.Input.URL != "" {
		parsed, err := url.Parse(r.Input.URL)
		if err != nil || strings.IndexFunc(r.Input.URL, unicode.IsSpace) >= 0 {
			return validationError("invalid_input_url", "The input must be an absolute HTTPS URL without whitespace.")
		}
		if parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
			return validationError("invalid_input_url", "The input must be an absolute HTTPS URL without user credentials.")
		}
		if port := parsed.Port(); port != "" {
			number, err := strconv.Atoi(port)
			if err != nil || number < 0 || number > 65535 {
				return validationError("invalid_input_url", "The input URL contains an invalid port.")
			}
		}
	}
	return nil
}
