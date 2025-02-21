package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/metadata"
)

// ExtensionClaims represents the claims in the JWT token for the extension.
type ExtensionClaims struct {
	jwt.RegisteredClaims
	Capabilities []extensions.CapabilityType `json:"cap,omitempty"`
}

// GenerateExtensionToken generates a JWT token for the extension.
func GenerateExtensionToken(extension *extensions.Extension, serverInfo *ServerInfo) (string, error) {
	claims := ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "azd",
			Subject:   extension.Id,
			Audience:  []string{serverInfo.Address},
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour * 1)),
		},
		Capabilities: extension.Capabilities,
	}

	jwtToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(serverInfo.SigningKey))
	if err != nil {
		return "", err
	}

	return jwtToken, nil
}

// ParseExtensionToken parses and validates the extension token.
func ParseExtensionToken(tokenValue string, serverInfo *ServerInfo) (*ExtensionClaims, error) {
	claims := &ExtensionClaims{}

	token, err := jwt.ParseWithClaims(tokenValue, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}

		return []byte(serverInfo.SigningKey), nil
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

// GetExtensionClaims retrieves the extension claims from the incoming gRPC context.
func GetExtensionClaims(ctx context.Context) (*ExtensionClaims, error) {
	md, ok := metadata.FromIncomingContext(ctx)
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
