// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/metadata"
)

// waitForDebugger checks if AZD_EXT_DEBUG environment variable is set to a truthy value.
// If set, prompts the user to attach a debugger to the current process.
func waitForDebugger(ctx context.Context, extensionId string, azdClient *AzdClient) {
	debugValue := os.Getenv("AZD_EXT_DEBUG")
	if debugValue == "" {
		return
	}

	isDebug, err := strconv.ParseBool(debugValue)
	if err != nil || !isDebug {
		return
	}

	message := fmt.Sprintf("Extension '%s' ready to debug (pid: %d).", extensionId, os.Getpid())

	_, err = azdClient.Prompt().Confirm(ctx, &ConfirmRequest{
		Options: &ConfirmOptions{
			Message:      message,
			DefaultValue: ux.Ptr(true),
		},
	})

	if err != nil {
		log.Printf("failed to prompt for debugger: %v\n", err)
	}
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
