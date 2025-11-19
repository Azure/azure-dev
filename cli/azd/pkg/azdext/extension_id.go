// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/metadata"
)

// GetExtensionId extracts the extension ID from the JWT token in the context metadata
func GetExtensionId(ctx context.Context) string {
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
