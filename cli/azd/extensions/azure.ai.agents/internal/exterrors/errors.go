// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package exterrors provides structured error helpers for the azure.ai.agents extension.
//
// Use plain Go errors until the current code can confidently choose a final
// category, code, and suggestion. At that point, create a structured error with
// one of the helpers in this package or with one of the Azure/gRPC conversion
// helpers.
//
// Once an error is structured, usually return it unchanged. Avoid wrapping a
// structured error with [fmt.Errorf] and %w for extra context: azd serializes the
// structured error's own message and metadata, not the outer wrapper text.
package exterrors

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
)

// InputSourceKind identifies a supported way to provide a required input.
type InputSourceKind = input.InputSourceKind

const (
	InputSourceFlag        = input.InputSourceFlag
	InputSourceEnvironment = input.InputSourceEnvironment
	InputSourceConfig      = input.InputSourceConfig
)

// InputSource describes one supported source for a required input.
type InputSource struct {
	Kind         InputSourceKind
	Name         string
	ExampleValue string
	Example      string
}

// RequiredInput describes a user-fixable input and all supported ways to provide it.
type RequiredInput struct {
	Name        string
	Description string
	Sources     []InputSource
}

// MissingInputError renders required-input guidance through the extension SDK's
// currently supported LocalError transport.
type MissingInputError struct {
	LocalError *azdext.LocalError
	cause      error
}

// Error implements the error interface.
func (e *MissingInputError) Error() string {
	return e.LocalError.Error()
}

// Unwrap exposes the LocalError transport and optional cause.
func (e *MissingInputError) Unwrap() []error {
	errs := []error{e.LocalError}
	if e.cause != nil {
		errs = append(errs, e.cause)
	}
	return errs
}

// WithCause preserves the lower-level failure that prevented input resolution.
func (e *MissingInputError) WithCause(cause error) *MissingInputError {
	e.cause = cause
	return e
}

// ---------------------------------------------------------------------------
// Structured error factories
// ---------------------------------------------------------------------------

// Validation returns a validation [azdext.LocalError] for user-input or manifest errors.
func Validation(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryValidation,
		Suggestion: suggestion,
	}
}

// Dependency returns a dependency [azdext.LocalError] for missing resources or services.
func Dependency(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryDependency,
		Suggestion: suggestion,
	}
}

// MissingInputValidation returns an actionable validation error for required user input.
func MissingInputValidation(code, message string, inputs ...RequiredInput) *MissingInputError {
	return newMissingInputError(azdext.LocalErrorCategoryValidation, code, message, inputs...)
}

// MissingInputDependency returns an actionable dependency error for required external state.
func MissingInputDependency(code, message string, inputs ...RequiredInput) *MissingInputError {
	return newMissingInputError(azdext.LocalErrorCategoryDependency, code, message, inputs...)
}

func newMissingInputError(
	category azdext.LocalErrorCategory,
	code string,
	message string,
	inputs ...RequiredInput,
) *MissingInputError {
	return &MissingInputError{
		LocalError: &azdext.LocalError{
			Message:    message,
			Code:       code,
			Category:   category,
			Suggestion: renderMissingInputSuggestion(inputs),
		},
	}
}

func renderMissingInputSuggestion(inputs []RequiredInput) string {
	var b strings.Builder
	b.WriteString("Provide the required input using one of the supported sources:")

	examples := make([]string, 0)
	seenExamples := map[string]struct{}{}
	for _, input := range inputs {
		b.WriteString("\n\n")
		b.WriteString(input.Name)
		if input.Description != "" {
			b.WriteString(": ")
			b.WriteString(input.Description)
		}

		for _, source := range input.Sources {
			b.WriteString("\n  - ")
			b.WriteString(string(source.Kind))
			b.WriteString(": ")
			b.WriteString(source.Name)
			if source.ExampleValue != "" {
				b.WriteString(" (example value: ")
				b.WriteString(source.ExampleValue)
				b.WriteString(")")
			}
			if source.Example != "" {
				if _, found := seenExamples[source.Example]; !found {
					seenExamples[source.Example] = struct{}{}
					examples = append(examples, source.Example)
				}
			}
		}
	}

	if len(examples) > 0 {
		b.WriteString("\n\nExamples:")
		for _, example := range examples {
			b.WriteString("\n  ")
			b.WriteString(example)
		}
	}

	return b.String()
}

// Compatibility returns a compatibility [azdext.LocalError] for version/feature mismatches.
func Compatibility(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryCompatibility,
		Suggestion: suggestion,
	}
}

// Auth returns an auth [azdext.LocalError] for authentication or authorization failures.
func Auth(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryAuth,
		Suggestion: suggestion,
	}
}

// Configuration returns a local/configuration [azdext.LocalError].
func Configuration(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryLocal,
		Suggestion: suggestion,
	}
}

// User returns a user-action [azdext.LocalError] (e.g. cancellation). No suggestion.
func User(code, message string) error {
	return &azdext.LocalError{
		Message:  message,
		Code:     code,
		Category: azdext.LocalErrorCategoryUser,
	}
}

// Internal returns an internal [azdext.LocalError] for unexpected extension failures. No suggestion.
func Internal(code, message string) error {
	return &azdext.LocalError{
		Message:  message,
		Code:     code,
		Category: azdext.LocalErrorCategoryInternal,
	}
}

// Service returns a structured service failure with operation-specific telemetry context.
func Service(operation, code, message, serviceName, suggestion string) *azdext.ServiceError {
	if code == "" {
		code = "failed"
	}
	return &azdext.ServiceError{
		Message:     message,
		ErrorCode:   fmt.Sprintf("%s.%s", operation, code),
		ServiceName: serviceName,
		Suggestion:  suggestion,
	}
}

// InternalFromError preserves an existing structured error or cancellation and otherwise
// classifies err as an internal failure with operation context.
func InternalFromError(err error, code, contextMessage string) error {
	if err == nil {
		return nil
	}
	if structured := structuredError(err); structured != nil {
		return structured
	}
	if IsCancellation(err) {
		return Cancelled(fmt.Sprintf("%s was cancelled", contextMessage))
	}
	return Internal(code, fmt.Sprintf("%s: %s", contextMessage, err))
}

// ---------------------------------------------------------------------------
// Azure / gRPC error converters
// ---------------------------------------------------------------------------

// ServiceFromAzure wraps an [azcore.ResponseError] into an [azdext.ServiceError] with operation context.
// It also preserves existing structured errors and authentication failures.
func ServiceFromAzure(err error, operation string) error {
	if err == nil {
		return nil
	}
	if structured := structuredError(err); structured != nil {
		return structured
	}
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		serviceName := ""
		if respErr.RawResponse != nil && respErr.RawResponse.Request != nil {
			serviceName = respErr.RawResponse.Request.Host
			if serviceName == "" && respErr.RawResponse.Request.URL != nil {
				serviceName = respErr.RawResponse.Request.URL.Hostname()
			}
		}
		code := respErr.ErrorCode
		if code == "" {
			code = fmt.Sprintf("%d", respErr.StatusCode)
		}
		return &azdext.ServiceError{
			Message:     fmt.Sprintf("%s: %s", operation, respErr.Error()),
			ErrorCode:   fmt.Sprintf("%s.%s", operation, code),
			StatusCode:  respErr.StatusCode,
			ServiceName: serviceName,
		}
	}
	if IsCancellation(err) {
		return Cancelled(fmt.Sprintf("%s was cancelled", operation))
	}
	if _, ok := errors.AsType[*azidentity.AuthenticationFailedError](err); ok {
		return Auth(CodeAuthFailed, err.Error(), "run `azd auth login` to authenticate")
	}
	return Internal(operation, fmt.Sprintf("%s: %s", operation, err.Error()))
}

// FromHost converts an error returned by an azd host gRPC service. Service metadata
// transported in gRPC details is preserved and prefixed with the current operation.
func FromHost(err error, operation, contextMessage string) error {
	if err == nil {
		return nil
	}
	if structured := structuredError(err); structured != nil {
		return structured
	}
	if IsCancellation(err) {
		return Cancelled(fmt.Sprintf("%s was cancelled", contextMessage))
	}

	st, ok := azdext.GRPCStatusFromError(err)
	if !ok {
		return Internal(operation, fmt.Sprintf("%s: %s", contextMessage, err))
	}
	if st.Code() == codes.Unauthenticated {
		return authFromGrpcMessage(st.Message())
	}

	actionable := azdext.ActionableErrorDetailFromError(err)
	if relayedErr := relayedExtensionErrorFromStatus(st); relayedErr != nil {
		return applyActionableErrorDetail(azdext.UnwrapError(relayedErr), actionable)
	}
	if serviceDetail := serviceErrorDetailFromStatus(st); serviceDetail != nil {
		code := serviceDetail.GetErrorCode()
		if code == "" && serviceDetail.GetStatusCode() > 0 {
			code = strconv.Itoa(int(serviceDetail.GetStatusCode()))
		}

		serviceErr := Service(
			operation,
			code,
			fmt.Sprintf("%s: %s", contextMessage, st.Message()),
			serviceDetail.GetServiceName(),
			"",
		)
		serviceErr.StatusCode = int(serviceDetail.GetStatusCode())
		if actionable != nil {
			serviceErr.Suggestion = actionable.GetSuggestion()
			serviceErr.Links = azdext.UnwrapErrorLinks(actionable.GetLinks())
		}
		return serviceErr
	}
	if actionable != nil {
		return err
	}

	return Internal(operation, fmt.Sprintf("%s: %s", contextMessage, st.Message()))
}

// FromAiService wraps a gRPC error returned by an azd host AI service call
// into a structured [azdext.LocalError]. It detects auth errors ([codes.Unauthenticated])
// and classifies them as Auth errors. For other errors, it preserves the server's
// ErrorInfo reason code (from the azd.ai domain) when available,
// falling back to the provided code.
func FromAiService(err error, fallbackCode string) error {
	if err == nil {
		return nil
	}

	if IsCancellation(err) {
		return Cancelled(err.Error())
	}

	st, ok := status.FromError(err)
	if !ok {
		return Internal(fallbackCode, err.Error())
	}

	if st.Code() == codes.Unauthenticated {
		return authFromGrpcMessage(st.Message())
	}

	code := fallbackCode
	if reason := aiErrorReason(st); reason != "" {
		code = reason
	}

	return Internal(code, st.Message())
}

// FromPrompt converts a gRPC error from an azd host Prompt call into a structured error.
// It renders actionable missing-input metadata emitted by newer hosts through the
// LocalError transport supported by the extension's currently pinned SDK.
func FromPrompt(err error, contextMsg string) error {
	if err == nil {
		return nil
	}
	if structured := structuredError(err); structured != nil {
		return structured
	}

	if IsCancellation(err) {
		return Cancelled(contextMsg)
	}

	st, ok := status.FromError(err)
	if ok && st.Code() == codes.Unauthenticated {
		return authFromGrpcMessage(fmt.Sprintf("%s: %s", contextMsg, st.Message()))
	}

	if ok {
		actionable := azdext.ActionableErrorDetailFromError(err)
		if metadata, found := promptRequiredMetadataFromActionable(actionable); found {
			message := metadata.Message
			if message == "" {
				message = st.Message()
			}
			missingInput := newMissingInputError(
				azdext.LocalErrorCategoryValidation,
				CodePromptFailed,
				fmt.Sprintf("%s: %s", contextMsg, message),
				metadata.Inputs...,
			)
			if actionable.GetSuggestion() != "" {
				missingInput.LocalError.Suggestion = actionable.GetSuggestion()
			}
			missingInput.LocalError.Links = azdext.UnwrapErrorLinks(actionable.GetLinks())
			return missingInput
		}
		if actionable != nil {
			return &azdext.LocalError{
				Message:    fmt.Sprintf("%s: %s", contextMsg, st.Message()),
				Code:       CodePromptFailed,
				Category:   azdext.LocalErrorCategoryValidation,
				Suggestion: actionable.GetSuggestion(),
				Links:      azdext.UnwrapErrorLinks(actionable.GetLinks()),
			}
		}
		return Internal(CodePromptFailed, fmt.Sprintf("%s: %s", contextMsg, st.Message()))
	}

	return Internal(CodePromptFailed, fmt.Sprintf("%s: %s", contextMsg, err))
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type promptRequiredMetadata struct {
	Inputs  []RequiredInput
	Message string
}

func promptRequiredMetadataFromActionable(
	actionable *azdext.ActionableErrorDetail,
) (promptRequiredMetadata, bool) {
	if actionable == nil {
		return promptRequiredMetadata{}, false
	}

	// The v1.31 SDK does not expose ActionableErrorDetail field 3. New hosts
	// serialize PromptRequiredErrorDetail there, so decode the preserved unknown
	// field until this module can take a merge-safe SDK upgrade.
	data := actionable.ProtoReflect().GetUnknown()
	for len(data) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(data)
		if tagLength < 0 {
			return promptRequiredMetadata{}, false
		}
		data = data[tagLength:]

		if number == 3 && wireType == protowire.BytesType {
			value, valueLength := protowire.ConsumeBytes(data)
			if valueLength < 0 {
				return promptRequiredMetadata{}, false
			}
			return parsePromptRequiredMetadata(value), true
		}

		valueLength := protowire.ConsumeFieldValue(number, wireType, data)
		if valueLength < 0 {
			return promptRequiredMetadata{}, false
		}
		data = data[valueLength:]
	}

	return promptRequiredMetadata{}, false
}

func parsePromptRequiredMetadata(data []byte) promptRequiredMetadata {
	metadata := promptRequiredMetadata{}
	for len(data) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(data)
		if tagLength < 0 {
			return metadata
		}
		data = data[tagLength:]

		switch {
		case number == 1 && wireType == protowire.BytesType:
			value, valueLength := protowire.ConsumeBytes(data)
			if valueLength < 0 {
				return metadata
			}
			metadata.Inputs = append(metadata.Inputs, parseRequiredInput(value))
			data = data[valueLength:]
		case number == 2 && wireType == protowire.BytesType:
			_, data = consumeStringField(data)
		case number == 3 && wireType == protowire.BytesType:
			metadata.Message, data = consumeStringField(data)
		default:
			valueLength := protowire.ConsumeFieldValue(number, wireType, data)
			if valueLength < 0 {
				return metadata
			}
			data = data[valueLength:]
		}
	}
	return metadata
}

func parseRequiredInput(data []byte) RequiredInput {
	input := RequiredInput{}
	for len(data) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(data)
		if tagLength < 0 {
			return input
		}
		data = data[tagLength:]

		switch {
		case number == 1 && wireType == protowire.BytesType:
			input.Name, data = consumeStringField(data)
		case number == 2 && wireType == protowire.BytesType:
			input.Description, data = consumeStringField(data)
		case number == 3 && wireType == protowire.BytesType:
			value, valueLength := protowire.ConsumeBytes(data)
			if valueLength < 0 {
				return input
			}
			input.Sources = append(input.Sources, parseInputSource(value))
			data = data[valueLength:]
		default:
			valueLength := protowire.ConsumeFieldValue(number, wireType, data)
			if valueLength < 0 {
				return input
			}
			data = data[valueLength:]
		}
	}
	return input
}

func parseInputSource(data []byte) InputSource {
	source := InputSource{}
	for len(data) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(data)
		if tagLength < 0 {
			return source
		}
		data = data[tagLength:]

		switch {
		case number == 1 && wireType == protowire.VarintType:
			value, valueLength := protowire.ConsumeVarint(data)
			if valueLength < 0 {
				return source
			}
			source.Kind = inputSourceKindFromProto(value)
			data = data[valueLength:]
		case number == 2 && wireType == protowire.BytesType:
			source.Name, data = consumeStringField(data)
		case number == 3 && wireType == protowire.BytesType:
			source.ExampleValue, data = consumeStringField(data)
		case number == 4 && wireType == protowire.BytesType:
			source.Example, data = consumeStringField(data)
		default:
			valueLength := protowire.ConsumeFieldValue(number, wireType, data)
			if valueLength < 0 {
				return source
			}
			data = data[valueLength:]
		}
	}
	return source
}

func consumeStringField(data []byte) (string, []byte) {
	value, valueLength := protowire.ConsumeString(data)
	if valueLength < 0 {
		return "", nil
	}
	return value, data[valueLength:]
}

func inputSourceKindFromProto(value uint64) InputSourceKind {
	switch value {
	case 1:
		return InputSourceFlag
	case 2:
		return InputSourceEnvironment
	case 3:
		return InputSourceConfig
	default:
		return ""
	}
}

// authFromGrpcMessage creates a structured Auth error from a gRPC Unauthenticated message.
// It classifies the error as not_logged_in, login_expired, or a generic auth_failed
// based on message content.
func authFromGrpcMessage(msg string) error {
	if strings.Contains(msg, "not logged in") {
		return Auth(CodeNotLoggedIn, msg, "run `azd auth login` to authenticate")
	}
	if strings.Contains(msg, "expired") {
		return Auth(CodeLoginExpired, msg, "run `azd auth login` to acquire a new token")
	}
	return Auth(CodeAuthFailed, msg, "run `azd auth login` to authenticate")
}

func structuredError(err error) error {
	if serviceErr, ok := errors.AsType[*azdext.ServiceError](err); ok {
		return serviceErr
	}
	if missingInputErr, ok := errors.AsType[*MissingInputError](err); ok {
		return missingInputErr
	}
	if localErr, ok := errors.AsType[*azdext.LocalError](err); ok {
		return localErr
	}
	return nil
}

// serviceErrorDetailFromStatus is kept local until azure.ai.agents can consume an
// azd version that includes azdext.ServiceErrorDetailFromStatus.
func serviceErrorDetailFromStatus(st *status.Status) *azdext.ServiceErrorDetail {
	if st == nil {
		return nil
	}
	for _, detail := range st.Details() {
		if serviceErr, ok := detail.(*azdext.ServiceErrorDetail); ok {
			return serviceErr
		}
	}
	return nil
}

func relayedExtensionErrorFromStatus(st *status.Status) *azdext.ExtensionError {
	if st == nil {
		return nil
	}
	for _, detail := range st.Details() {
		if relayedErr, ok := detail.(*azdext.ExtensionError); ok {
			return relayedErr
		}
	}
	return nil
}

func applyActionableErrorDetail(err error, actionable *azdext.ActionableErrorDetail) error {
	if actionable == nil {
		return err
	}

	if serviceErr, ok := errors.AsType[*azdext.ServiceError](err); ok {
		if actionable.GetSuggestion() != "" {
			serviceErr.Suggestion = actionable.GetSuggestion()
		}
		if len(actionable.GetLinks()) > 0 {
			serviceErr.Links = azdext.UnwrapErrorLinks(actionable.GetLinks())
		}
		return serviceErr
	}
	if localErr, ok := errors.AsType[*azdext.LocalError](err); ok {
		if actionable.GetSuggestion() != "" {
			localErr.Suggestion = actionable.GetSuggestion()
		}
		if len(actionable.GetLinks()) > 0 {
			localErr.Links = azdext.UnwrapErrorLinks(actionable.GetLinks())
		}
		return localErr
	}

	return err
}

// IsCancellation checks if an error represents user cancellation
// ([context.Canceled] or gRPC [codes.Canceled]).
func IsCancellation(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	if st, ok := status.FromError(err); ok && st.Code() == codes.Canceled {
		return true
	}
	return false
}

// IsPromptRequired reports whether err is a `--no-prompt` failure propagated
// from the azd host. New hosts attach structured metadata; older hosts only
// expose the PromptRequiredError text in the gRPC status message.
func IsPromptRequired(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := errors.AsType[*input.PromptRequiredError](err); ok {
		return true
	}
	if _, found := promptRequiredMetadataFromActionable(azdext.ActionableErrorDetailFromError(err)); found {
		return true
	}
	if st, ok := status.FromError(err); ok {
		return strings.Contains(strings.ToLower(st.Message()), "prompt required")
	}
	return strings.Contains(strings.ToLower(err.Error()), "prompt required")
}

// Cancelled returns a user cancellation error.
func Cancelled(message string) error {
	return User(CodeCancelled, message)
}

// aiErrorReason extracts the ErrorInfo.Reason from a gRPC status
// when the domain matches [azdext.AiErrorDomain].
func aiErrorReason(st *status.Status) string {
	for _, detail := range st.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if ok && info.Domain == azdext.AiErrorDomain {
			return info.Reason
		}
	}
	return ""
}
