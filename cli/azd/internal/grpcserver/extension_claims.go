package grpcserver

import (
	"errors"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/golang-jwt/jwt/v5"
)

type ExtensionClaims struct {
	jwt.RegisteredClaims
	Capabilities []extensions.CapabilityType `json:"cap,omitempty"`
}

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
