// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// ExtensionClaims represents the claims in the JWT token for the extension.
type ExtensionClaims struct {
	jwt.RegisteredClaims
	Capabilities []CapabilityType `json:"cap,omitempty"`
}

// extensionClaimsKeyType is the context key for storing validated extension claims.
type extensionClaimsKeyType struct{}

var extensionClaimsKey = extensionClaimsKeyType{}

// WithClaimsContext returns a new context with the validated extension claims stored in it.
func WithClaimsContext(ctx context.Context, claims *ExtensionClaims) context.Context {
	return context.WithValue(ctx, extensionClaimsKey, claims)
}

// GetClaimsFromContext retrieves validated extension claims from the context.
// Claims must have been stored by the gRPC auth interceptor via WithClaimsContext.
func GetClaimsFromContext(ctx context.Context) (*ExtensionClaims, error) {
	claims, ok := ctx.Value(extensionClaimsKey).(*ExtensionClaims)
	if !ok || claims == nil {
		return nil, fmt.Errorf("no validated extension claims found in context")
	}

	return claims, nil
}
