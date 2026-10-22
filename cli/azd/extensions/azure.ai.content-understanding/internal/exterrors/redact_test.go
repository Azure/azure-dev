// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSensitiveClassRedaction(t *testing.T) {
	for _, message := range []string{
		"Authorization: Bearer private-token",
		"Authorization=CustomScheme private-token extra-private-values",
		"Proxy-Authorization: Basic private-credentials",
		"api_key=private-key",
		"password: private-password",
		"access_token: private-token",
		"sig=private-signature",
		"schema: {\"customer\":\"private-schema\"}",
		"content: private-extracted-text",
		"payload: private-body",
		"request https://private-user:private-pass@storage.test/private-doc.pdf?sig=private-sas#private-fragment",
		"file C:\\private-folder\\private-doc.pdf",
		"file \\\\private-server\\private-share\\private-doc.pdf",
		"file /private-folder/private-doc.pdf",
		"file ../private-folder/private-doc.pdf",
		"invalid value \"private-customer-value\"",
		"fieldSchema.fields.private-customer-key.type: invalid",
	} {
		t.Run(message, func(t *testing.T) {
			safe := Redact(message)
			assert.NotContains(t, safe, "private")
			assert.LessOrEqual(t, len(safe), MaxDiagnosticBytes)
		})
	}
	assert.Equal(t, "Safe operation failed.", Redact("Safe operation failed."))
	assert.NotContains(t, Redact("safe\x1b[31m"), "\x1b")
	for _, message := range []string{strings.Repeat("private", 1000), string([]byte{0xff, 0xfe})} {
		assert.NotContains(t, Redact(message), "private")
		assert.LessOrEqual(t, len(Redact(message)), MaxDiagnosticBytes)
	}
}

func TestRedactionAtStructuredErrorBoundary(t *testing.T) {
	host, err := status.New(codes.FailedPrecondition, "path: /private-doc").WithDetails(
		&azdext.ActionableErrorDetail{Suggestion: "Authorization: Custom private-token"})
	require.NoError(t, err)
	for _, original := range []error{
		&azdext.LocalError{Code: "safe_code", Category: azdext.LocalErrorCategoryValidation,
			Message: "schema: private-schema", Suggestion: "path: /private-file"},
		&azdext.ServiceError{ErrorCode: "safe_code", StatusCode: 409,
			Message: "Authorization: Custom private-token", Suggestion: "content: private-content"},
		&azdext.ToolError{ToolName: "azd", Kind: azdext.ToolErrorKindFailed,
			Message: "payload: private-payload", Suggestion: "path: C:\\private-file"},
		&cuclient.Error{Kind: cuclient.ErrorService, Code: "safe_code",
			Message: "Authorization: Custom private-token", Suggestion: "schema: private-schema"},
		host.Err(),
	} {
		safe := FromError(original)
		assert.NotContains(t, safe.Error(), "private")
		body, err := json.Marshal(azdext.WrapError(safe))
		require.NoError(t, err)
		assert.NotContains(t, string(body), "private")
	}
}

func TestDiagnosticMetadataAndCancellationRemainSafe(t *testing.T) {
	for _, original := range []error{
		&azdext.LocalError{Code: "safe_code", Message: "Safe failure.", CauseTypes: []string{"private-path"},
			Links: []errorhandler.ErrorLink{{URL: "https://private-host/private-path?sig=private-token"}}},
		&azdext.ServiceError{ErrorCode: "safe_code", Message: "Safe failure.", ServiceName: "private-host"},
		&azdext.ServiceError{ErrorCode: "https://private-host", Message: "Safe failure."},
	} {
		body, err := json.Marshal(azdext.WrapError(FromError(original)))
		require.NoError(t, err)
		assert.NotContains(t, string(body), "private")
	}
	cancelled := &azdext.ToolError{ToolName: "azd", Message: "content: private-content", Err: context.Canceled}
	safe := FromError(cancelled)
	assert.ErrorIs(t, safe, context.Canceled)
	assert.NotContains(t, safe.Error(), "private")
}
