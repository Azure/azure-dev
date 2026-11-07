// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package schema

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"path/filepath"
	"slices"
	"strings"

	"azure.ai.content-understanding/internal/analysis"
	"azure.ai.content-understanding/internal/cuclient"
)

const SuggestionAnalyzer = "prebuilt-documentFieldSchema"

// AuthorOptions contains host-neutral schema authoring choices.
type AuthorOptions struct {
	Name, Base, Modality, Type, Sample string
	FromTemplate                       bool
}

// FieldSchema retains arbitrary suggested fields without losing unknown properties or numbers.
type FieldSchema struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	Fields      map[string]json.RawMessage `json:"fields"`
}

// TemplateConfig contains the shared extraction and classification settings.
type TemplateConfig struct {
	EstimateFieldSourceAndConfidence bool                        `json:"estimateFieldSourceAndConfidence"`
	EnableSegment                    bool                        `json:"enableSegment,omitempty"`
	ContentCategories                map[string]TemplateCategory `json:"contentCategories,omitempty"`
}

// TemplateCategory starts with a description, not a dependency on another analyzer.
type TemplateCategory struct {
	Description string `json:"description"`
}

// TemplateModels names authoring models without reading or changing service deployments.
type TemplateModels struct {
	Completion string `json:"completion"`
	Embedding  string `json:"embedding"`
}

// Definition is the standalone analyzer document returned by both authoring modes.
type Definition struct {
	APIVersion     string         `json:"apiVersion"`
	AnalyzerID     string         `json:"analyzerId"`
	Description    string         `json:"description"`
	BaseAnalyzerID string         `json:"baseAnalyzerId"`
	FieldSchema    *FieldSchema   `json:"fieldSchema,omitempty"`
	Config         TemplateConfig `json:"config"`
	Models         TemplateModels `json:"models"`
}

// Suggestion separates diagnostics and empty-result state from the analyzer document.
type Suggestion struct {
	Definition  Definition
	FoundFields bool
	Warnings    []cuclient.Diagnostic
}

// Validate enforces shared authoring choices before opening a service session.
func (options AuthorOptions) Validate() error {
	if err := ValidateCustomAnalyzerID(strings.TrimSpace(options.Name)); err != nil {
		return err
	}
	if !slices.Contains([]string{"document", "image", "audio", "video"}, options.Modality) {
		return invalid("modality", "Choose document, image, audio, or video.")
	}
	if !slices.Contains([]string{"extraction", "classification"}, options.Type) {
		return invalid("type", "Choose extraction or classification.")
	}
	if options.Base != "" {
		if err := cuclient.ValidateAnalyzerID(options.Base); err != nil {
			return err
		}
	}
	if options.Sample != "" && (options.FromTemplate || options.Type != "extraction" ||
		options.Modality != "document" || options.Base != "") {
		return invalid("from-sample",
			"A sample cannot be combined with --from-template, --type classification, a non-document modality, or --base.")
	}
	return nil
}

// Starter follows cu-cli-core's offline authoring templates, not service defaults.
func Starter(options AuthorOptions, version string) (Definition, error) {
	if err := options.Validate(); err != nil {
		return Definition{}, err
	}
	version, err := cuclient.NormalizeAPIVersion(version)
	if err != nil {
		return Definition{}, err
	}
	name := strings.TrimSpace(options.Name)
	base := options.Base
	if base == "" {
		base = "prebuilt-" + options.Modality
	}
	result := Definition{
		APIVersion: version, AnalyzerID: name, BaseAnalyzerID: base,
		Config: TemplateConfig{EstimateFieldSourceAndConfidence: true},
		Models: TemplateModels{Completion: "gpt-5.2", Embedding: "text-embedding-3-large"},
	}
	if options.Type == "classification" {
		result.Description = "TODO: classify inputs into the categories below; give each a clear " +
			"description. Optional: add \"analyzerId\": \"<existing-analyzer-id>\" to a " +
			"category to route matching content to that analyzer for extraction " +
			"(the analyzer \u2014 prebuilt like prebuilt-invoice or a custom one \u2014 must " +
			"already exist, otherwise create fails with InvalidAnalyzerId)."
		result.Config.EnableSegment = true
		result.Config.ContentCategories = map[string]TemplateCategory{
			"invoice": {Description: "Vendor invoices requesting payment for goods or services, typically " +
				"including vendor details, line items, totals, and payment terms."},
			"purchase_order": {Description: "Purchase orders that authorize a purchase, typically including a PO " +
				"number, buyer/supplier details, ordered items, quantities, and prices."},
			"receipt": {Description: "Retail or expense receipts confirming a completed payment, typically " +
				"including merchant, date, purchased items, and total paid."},
			"other": {Description: "Fallback category for content that does not match any category above."},
		}
		return result, nil
	}
	result.Description = "TODO: one-sentence description of what this " + options.Modality + " analyzer extracts."
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(extractionFields), &fields); err != nil {
		return Definition{}, err
	}
	result.FieldSchema = &FieldSchema{
		Name:        strings.ReplaceAll(name, "-", "_") + "_schema",
		Description: "TODO: one-sentence summary of the extraction.", Fields: fields,
	}
	return result, nil
}

// ValidateSample keeps the shared document-only sample format allowlist.
func ValidateSample(path string) error {
	extension := strings.ToLower(filepath.Ext(path))
	if !slices.Contains([]string{
		".pdf", ".tiff", ".docx", ".xlsx", ".pptx", ".docm", ".xlsm", ".pptm", ".doc", ".xls", ".ppt",
		".odt", ".ods", ".odp", ".epub", ".txt", ".html", ".md", ".rtf", ".xml", ".json", ".csv", ".tsv",
		".kml", ".eml", ".msg",
	}, extension) {
		return invalid("from-sample", "Select one document sample, for example PDF, DOCX, PPTX, XLSX, TXT, or HTML.")
	}
	return nil
}

// Suggest derives fields from one local sample; an empty suggestion explicitly retains the starter.
func Suggest(
	ctx context.Context, client cuclient.Client, input analysis.Input, options AuthorOptions, version string,
) (Suggestion, error) {
	if err := ctx.Err(); err != nil {
		return Suggestion{}, err
	}
	definition, err := Starter(options, version)
	if err != nil {
		return Suggestion{}, err
	}
	if options.Sample == "" || input.URL != "" || client == nil {
		return Suggestion{}, invalid("from-sample", "Specify one regular local document sample.")
	}
	if err := ValidateSample(options.Sample); err != nil {
		return Suggestion{}, err
	}
	file, err := input.Open()
	if err != nil {
		return Suggestion{}, err
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(options.Sample)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	contentType, _, _ = strings.Cut(contentType, ";")
	var response cuclient.AnalyzeResponse
	runErr := ctx.Err()
	if runErr == nil {
		response, runErr = client.Analyze(ctx, cuclient.AnalyzeRequest{
			AnalyzerID: SuggestionAnalyzer, Mode: cuclient.AnalysisModeAsync,
			Input: cuclient.Input{Body: file, ContentType: contentType},
		})
	}
	if err := errors.Join(runErr, ctx.Err(), file.Close()); err != nil {
		return Suggestion{}, err
	}
	if response.Result.Contents == nil {
		return Suggestion{}, &cuclient.Error{Kind: cuclient.ErrorService, Code: "invalid_schema_suggestion",
			Message: "The CU client returned an invalid schema-suggestion result."}
	}
	suggestion := Suggestion{Definition: definition, Warnings: response.Result.Warnings}
	for _, content := range response.Result.Contents {
		field, ok := object(content.Fields["schema"])
		if !ok {
			continue
		}
		fields, ok := object(field["valueJson"])
		if !ok || len(fields) == 0 {
			continue
		}
		for name, raw := range fields {
			fields[name], err = describeField(raw, "TODO: describe the '"+name+"' field.")
			if err != nil {
				return Suggestion{}, err
			}
		}
		suggestion.Definition.FieldSchema.Fields = fields
		suggestion.Definition.Description = "Suggested from sample '" + filepath.Base(options.Sample) +
			"' using prebuilt-documentFieldSchema."
		suggestion.Definition.FieldSchema.Description = "Suggested extraction schema derived from one sample document."
		suggestion.FoundFields = true
		break
	}
	return suggestion, nil
}

func describeField(raw json.RawMessage, placeholder string) (json.RawMessage, error) {
	definition, ok := object(raw)
	if !ok {
		return raw, nil
	}
	description := definition["description"]
	value, isText := text(description)
	if !present(description) || (isText && strings.TrimSpace(value) == "") {
		encoded, err := json.Marshal(placeholder)
		if err != nil {
			return nil, err
		}
		definition["description"] = encoded
	}
	if properties, ok := object(definition["properties"]); ok {
		for name, raw := range properties {
			updated, err := describeField(raw, "TODO: describe the '"+name+"' field.")
			if err != nil {
				return nil, err
			}
			properties[name] = updated
		}
		encoded, err := json.Marshal(properties)
		if err != nil {
			return nil, err
		}
		definition["properties"] = encoded
	}
	if _, ok := object(definition["items"]); ok {
		updated, err := describeField(definition["items"], "TODO: describe one item in this array.")
		if err != nil {
			return nil, err
		}
		definition["items"] = updated
	}
	return json.Marshal(definition)
}

const extractionFields = `{
  "example_string_field": {
    "type": "string", "method": "extract",
    "description": "TODO: replace this field. Describe specifically what to extract, ` +
	`where it appears in the document, and any formatting expectations. ` +
	`Example: 'Full legal name of the vendor as printed in the invoice header.'",
    "estimateSourceAndConfidence": true
  },
  "example_number_field": {
    "type": "number", "method": "extract",
    "description": "TODO: replace this field. Use for amounts, totals, rates, or ` +
	`other numeric values. Example: 'Invoice total amount before tax.'",
    "estimateSourceAndConfidence": true
  },
  "example_summary": {
    "type": "string", "method": "generate",
    "description": "Provide a one-line summary of the document's main purpose and key outcome. Keep it concise and factual."
  },
  "example_classify_field": {
    "type": "string", "method": "classify",
    "description": "TODO: replace this field. Use ` + "`classify` only when the value comes " +
	"from a closed set. Always include `enum`." + `",
    "enum": ["option_a", "option_b", "other"],
    "enumDescriptions": {
      "option_a": "Primary category when the content strongly matches pattern A.",
      "option_b": "Secondary category when the content matches pattern B.",
      "other": "Fallback category when neither option_a nor option_b applies."
    }
  },
  "example_table_field": {
    "type": "array", "method": "extract",
    "description": "TODO: replace this field. Use for repeating table rows such as ` +
	`line items, transactions, or schedule entries.",
    "items": {
      "type": "object", "description": "One extracted table row.",
      "properties": {
        "column_description": {
          "type": "string", "method": "extract", "description": "Text content for the description column."
        },
        "column_amount": {
          "type": "number", "method": "extract", "description": "Numeric value in the amount column."
        },
        "column_category": {
          "type": "string", "method": "classify",
          "description": "Classify the column value into a stable category used by downstream business logic.",
          "enum": ["product", "service", "fee", "tax", "other"],
          "enumDescriptions": {
            "product": "Physical good or inventory item.", "service": "Labor or service charge.",
            "fee": "Non-tax fee such as handling or processing.", "tax": "Tax line item.",
            "other": "Row does not fit the predefined categories."
          }
        }
      }
    }
  }
}`
