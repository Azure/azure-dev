// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ErrDebuggerAborted is returned when the user declines to attach a debugger.
var ErrDebuggerAborted = errors.New("debugger attach aborted")

// WaitForDebugger checks if AZD_EXT_DEBUG environment variable is set to a truthy value.
// If set, prompts the user to attach a debugger to the current process.
// This should be called at the start of extension command implementations to enable debugging.
//
// Returns nil if debugging is not enabled or if user confirms.
//
// Returns [ErrDebuggerAborted] if the user declines to attach a debugger.
// Returns [context.Canceled] if the user cancels the prompt (e.g., via Ctrl+C).
func WaitForDebugger(ctx context.Context, azdClient *AzdClient) error {
	debugValue := os.Getenv("AZD_EXT_DEBUG")
	if debugValue == "" {
		return nil
	}

	isDebug, err := strconv.ParseBool(debugValue)
	if err != nil || !isDebug {
		return nil
	}

	extensionId := getExtensionId(ctx)
	message := fmt.Sprintf("Extension '%s' ready to debug (pid: %d).", extensionId, os.Getpid())

	response, err := azdClient.Prompt().Confirm(ctx, &ConfirmRequest{
		Options: &ConfirmOptions{
			Message:      message,
			DefaultValue: ux.Ptr(true),
		},
	})

	if err != nil {
		// Check if the error is due to context cancellation (Ctrl+C)
		if status.Code(err) == codes.Canceled || errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		return fmt.Errorf("failed to prompt for debugger: %w", err)
	}

	// If user selected 'N', abort
	if !response.GetValue() {
		return ErrDebuggerAborted
	}

	return nil
}

// getExtensionId extracts the extension ID from the JWT token in the context metadata
func getExtensionId(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md, ok = metadata.FromOutgoingContext(ctx)
	}

	if !ok {
		return ""
	}

	authHeaders := md.Get("authorization")
	if len(authHeaders) == 0 {
		return ""
	}

	tokenString := authHeaders[0]
	if tokenString == "" {
		return ""
	}

	// Parse the JWT token without validation (we just need the subject claim)
	token, _, err := jwt.NewParser().ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return ""
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok {
		if sub, ok := claims["sub"].(string); ok {
			return sub
		}
	}

	return ""
}
