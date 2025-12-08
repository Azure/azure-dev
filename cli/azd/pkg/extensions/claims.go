// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/metadata"
)

// ExtensionClaims represents the claims in the JWT token for the extension.
type ExtensionClaims struct {
	jwt.RegisteredClaims
	Capabilities []CapabilityType `json:"cap,omitempty"`
}

// GetClaimsFromContext retrieves the extension claims from the incoming gRPC context.
func GetClaimsFromContext(ctx context.Context) (*ExtensionClaims, error) {
	// First check the incoming context
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		// Otherwise check the outgoing context
		md, ok = metadata.FromOutgoingContext(ctx)
	}

	if !ok {
		return nil, fmt.Errorf("failed to get metadata from context")
	}

	authHeaders := md.Get("authorization")
	if len(authHeaders) == 0 {
		return nil, fmt.Errorf("missing authorization header")
	}

	tokenValue := authHeaders[0]
	if tokenValue == "" {
		return nil, fmt.Errorf("missing token value")
	}

	claims := &ExtensionClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(tokenValue, claims)
	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	return claims, nil
}
