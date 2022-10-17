package identity

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

type contextKey string

const credentialsContextKey contextKey = "azure_credentials"

// Gets credentials for Azure from current context or panics if none are present.
func GetCredentials(ctx context.Context) azcore.TokenCredential {
	cred, ok := ctx.Value(credentialsContextKey).(azcore.TokenCredential)

	if !ok {
		panic("GetCredentials: no credentials in context")
	}

	return cred
}

// Sets the specified Azure token credential in context and returns new context
func WithCredentials(ctx context.Context, cred azcore.TokenCredential) context.Context {
	return context.WithValue(ctx, credentialsContextKey, cred)
}
