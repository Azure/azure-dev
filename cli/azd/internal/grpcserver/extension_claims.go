// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/golang-jwt/jwt/v5"
	"go.opentelemetry.io/otel/propagation"
)

// GenerateExtensionToken generates a JWT token for the extension.
func GenerateExtensionToken(extension *extensions.Extension, serverInfo *ServerInfo) (string, error) {
	return GenerateExtensionTokenWithContext(context.Background(), extension, serverInfo)
}

// GenerateExtensionTokenWithContext signs host trace context into the
// signed claims so extensions cannot choose a different trace.
func GenerateExtensionTokenWithContext(
	ctx context.Context,
	extension *extensions.Extension,
	serverInfo *ServerInfo,
) (string, error) {
	traceContext := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, traceContext)

	claims := extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "azd",
			Subject:   extension.Id,
			Audience:  []string{serverInfo.Address},
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour * 1)),
		},
		Capabilities: extension.Capabilities,
		Traceparent:  traceContext["traceparent"],
		Tracestate:   traceContext["tracestate"],
	}

	jwtToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(serverInfo.SigningKey)
	if err != nil {
		return "", err
	}

	return jwtToken, nil
}

// ParseExtensionToken parses and validates the extension token.
func ParseExtensionToken(tokenValue string, serverInfo *ServerInfo) (*extensions.ExtensionClaims, error) {
	claims := &extensions.ExtensionClaims{}

	token, err := jwt.ParseWithClaims(tokenValue, claims, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}

		return serverInfo.SigningKey, nil
	}, jwt.WithAudience(serverInfo.Address), jwt.WithIssuer("azd"))

	if err != nil {
		return nil, fmt.Errorf("token validation failed: %w", err)
	}

	if !token.Valid {
		return nil, errors.New("token validation failed.")
	}

	if claims.ExpiresAt.Before(time.Now()) {
		return nil, errors.New("token has expired")
	}

	return claims, nil
}
