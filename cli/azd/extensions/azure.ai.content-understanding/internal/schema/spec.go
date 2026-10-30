// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package schema

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"slices"
	"strings"

	"azure.ai.content-understanding/internal/cuclient"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed specs/*.json
var contracts embed.FS

type offlineContractLoader struct{}

func (offlineContractLoader) Load(string) (any, error) {
	return nil, errors.New("external contract resolution is disabled")
}

func validateSpec(data []byte, version string) ([]Finding, error) {
	if version != cuclient.DefaultAPIVersion && version != cuclient.PreviewAPIVersion {
		return nil, &cuclient.Error{
			Kind: cuclient.ErrorValidation, Code: "unsupported_validation_contract",
			Message:    "No bundled analyzer validation contract exists for the selected preview version.",
			Suggestion: "Omit --spec for authoring checks, or select 2025-11-01 or 2026-06-01-preview.",
		}
	}
	raw, err := contracts.ReadFile("specs/" + version + ".json")
	if err != nil {
		return nil, contractError()
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, contractError()
	}
	return validateContract(data, document, version)
}

func validateContract(data []byte, document any, version string) ([]Finding, error) {
	const location = "urn:cu-analyzer-create"
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(offlineContractLoader{})
	compiler.DefaultDraft(jsonschema.Draft4)
	if err := compiler.AddResource(location, document); err != nil {
		return nil, contractError()
	}
	compiled, err := compiler.Compile(location)
	if err != nil {
		return nil, contractError()
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, contractError()
	}
	err = compiled.Validate(instance)
	if err == nil {
		return []Finding{}, nil
	}
	failure, ok := errors.AsType[*jsonschema.ValidationError](err)
	if !ok {
		return nil, contractError()
	}
	findings := []Finding{}
	var collect func(*jsonschema.ValidationError)
	collect = func(failure *jsonschema.ValidationError) {
		if len(failure.Causes) != 0 {
			for _, child := range failure.Causes {
				collect(child)
			}
			return
		}
		path := strings.Join(failure.InstanceLocation, ".")
		if path == "" {
			path = "$"
		}
		// Validator messages can echo customer values. Report the constraint,
		// not the rejected instance or the validator's complete error string.
		findings = append(findings, Finding{path, fmt.Sprintf(
			"Violates the CU %s service contract (%s).", version, strings.Join(failure.ErrorKind.KeywordPath(), "/"),
		)})
	}
	collect(failure)
	slices.SortFunc(findings, func(a, b Finding) int {
		if n := strings.Compare(a.Path, b.Path); n != 0 {
			return n
		}
		return strings.Compare(a.Message, b.Message)
	})
	return findings, nil
}

func contractError() error {
	return &cuclient.Error{
		Kind: cuclient.ErrorService, Code: "invalid_validation_contract",
		Message:    "The bundled analyzer validation contract could not be loaded or compiled.",
		Suggestion: "Reinstall the extension or report this error code. Spec validation was not skipped.",
	}
}
