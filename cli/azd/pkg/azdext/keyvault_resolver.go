// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
)

// KeyVaultResolver resolves Azure Key Vault secret references for extension
// scenarios. It uses the extension's [TokenProvider] for authentication and
// the Azure SDK data-plane client for secret retrieval.
//
// Two reference formats are supported:
//
//	akvs://<subscription-id>/<vault-name>/<secret-name>
//	@Microsoft.KeyVault(SecretUri=https://<vault>.vault.azure.net/secrets/<secret>[/<version>])
//
// The akvs:// scheme is the preferred compact form. The @Microsoft.KeyVault
// format supports only the SecretUri= variant; the VaultName/SecretName form
// is not currently implemented.
//
// Usage:
//
//	tp, _ := azdext.NewTokenProvider(ctx, client, nil)
//	resolver, _ := azdext.NewKeyVaultResolver(tp, nil)
//	value, err := resolver.Resolve(ctx, "akvs://sub-id/my-vault/my-secret")
type KeyVaultResolver struct {
	credential    azcore.TokenCredential
	clientFactory secretClientFactory
	opts          KeyVaultResolverOptions
	clientCache   sync.Map // map[vaultURL]secretGetter — per-vault client cache
}

// secretClientFactory abstracts secret client creation for testability.
type secretClientFactory func(vaultURL string, credential azcore.TokenCredential) (secretGetter, error)

// secretGetter abstracts the Azure SDK secret client's GetSecret method.
type secretGetter interface {
	GetSecret(
		ctx context.Context,
		name string,
		version string,
		options *azsecrets.GetSecretOptions,
	) (azsecrets.GetSecretResponse, error)
}

// KeyVaultResolverOptions configures a [KeyVaultResolver].
type KeyVaultResolverOptions struct {
	// VaultSuffix overrides the default Key Vault DNS suffix.
	// Defaults to "vault.azure.net" (Azure public cloud).
	VaultSuffix string

	// ClientFactory overrides the default secret client constructor.
	// Useful for testing. When nil, the production [azsecrets.NewClient] is used.
	ClientFactory func(vaultURL string, credential azcore.TokenCredential) (secretGetter, error)
}

// NewKeyVaultResolver creates a [KeyVaultResolver] with the given credential.
//
// credential must not be nil; it is typically a [*TokenProvider].
// If opts is nil, production defaults are used.
func NewKeyVaultResolver(credential azcore.TokenCredential, opts *KeyVaultResolverOptions) (*KeyVaultResolver, error) {
	if credential == nil {
		return nil, errors.New("azdext.NewKeyVaultResolver: credential must not be nil")
	}

	if opts == nil {
		opts = &KeyVaultResolverOptions{}
	}

	if opts.VaultSuffix == "" {
		opts.VaultSuffix = "vault.azure.net"
	}

	factory := defaultSecretClientFactory
	if opts.ClientFactory != nil {
		factory = opts.ClientFactory
	}

	return &KeyVaultResolver{
		credential:    credential,
		clientFactory: factory,
		opts:          *opts,
	}, nil
}

// defaultSecretClientFactory creates a real Azure SDK secrets client.
func defaultSecretClientFactory(vaultURL string, credential azcore.TokenCredential) (secretGetter, error) {
	client, err := azsecrets.NewClient(vaultURL, credential, &azsecrets.ClientOptions{
		DisableChallengeResourceVerification: false,
	})
	if err != nil {
		return nil, err
	}

	return client, nil
}

// Resolve fetches the secret value for a Key Vault secret reference.
//
// Both akvs:// and @Microsoft.KeyVault(SecretUri=...) formats are accepted.
//
// Returns a [*KeyVaultResolveError] for all domain errors (invalid reference,
// secret not found, authentication failure). No silent fallbacks or hidden retries.
func (r *KeyVaultResolver) Resolve(ctx context.Context, ref string) (string, error) {
	if ctx == nil {
		return "", errors.New("azdext.KeyVaultResolver.Resolve: context must not be nil")
	}

	parsed, err := ParseSecretReference(ref)
	if err != nil {
		return "", &KeyVaultResolveError{
			Reference: ref,
			Reason:    ResolveReasonInvalidReference,
			Err:       err,
		}
	}

	vaultURL := parsed.VaultURL
	if vaultURL == "" {
		vaultURL = fmt.Sprintf("https://%s.%s", parsed.VaultName, r.opts.VaultSuffix)
	}

	secretVersion := parsed.SecretVersion

	client, err := r.getOrCreateClient(vaultURL)
	if err != nil {
		return "", &KeyVaultResolveError{
			Reference: ref,
			Reason:    ResolveReasonClientCreation,
			Err:       fmt.Errorf("failed to create Key Vault client for %s: %w", vaultURL, err),
		}
	}

	resp, err := client.GetSecret(ctx, parsed.SecretName, secretVersion, nil)
	if err != nil {
		// Default to ServiceError for non-ResponseError failures (e.g., network
		// timeouts, DNS resolution failures). AccessDenied is only used when the
		// server explicitly returns 401/403.
		reason := ResolveReasonServiceError

		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) {
			switch respErr.StatusCode {
			case http.StatusNotFound:
				reason = ResolveReasonNotFound
			case http.StatusForbidden, http.StatusUnauthorized:
				reason = ResolveReasonAccessDenied
			default:
				reason = ResolveReasonServiceError
			}
		}

		return "", &KeyVaultResolveError{
			Reference: ref,
			Reason:    reason,
			Err: fmt.Errorf(
				"failed to retrieve secret %q from vault %q: %w",
				parsed.SecretName,
				parsed.VaultName,
				err,
			),
		}
	}

	if resp.Value == nil {
		return "", &KeyVaultResolveError{
			Reference: ref,
			Reason:    ResolveReasonNotFound,
			Err:       fmt.Errorf("secret %q in vault %q has a nil value", parsed.SecretName, parsed.VaultName),
		}
	}

	return *resp.Value, nil
}

// getOrCreateClient returns a cached client for the given vault URL, creating
// one via the client factory if no cached entry exists. The cache is safe for
// concurrent use.
func (r *KeyVaultResolver) getOrCreateClient(vaultURL string) (secretGetter, error) {
	if cached, ok := r.clientCache.Load(vaultURL); ok {
		return cached.(secretGetter), nil
	}

	client, err := r.clientFactory(vaultURL, r.credential)
	if err != nil {
		return nil, err
	}

	// Store-or-load to handle concurrent creation for the same vault.
	actual, _ := r.clientCache.LoadOrStore(vaultURL, client)
	return actual.(secretGetter), nil
}

// ResolveMap resolves a map of key → secret references, returning a map of
// key → resolved secret values. Both akvs:// and @Microsoft.KeyVault formats
// are accepted. All entries are attempted; errors are collected and returned
// together via [errors.Join] so that callers see every failure at once.
//
// Non-secret values are passed through unchanged, so callers can safely
// resolve a mixed map of plain values and secret references.
//
// Keys are processed in sorted order so that error messages are deterministic.
func (r *KeyVaultResolver) ResolveMap(ctx context.Context, refs map[string]string) (map[string]string, error) {
	if ctx == nil {
		return nil, errors.New("azdext.KeyVaultResolver.ResolveMap: context must not be nil")
	}

	result := make(map[string]string, len(refs))

	// Sort keys for deterministic iteration and error reporting.
	keys := make([]string, 0, len(refs))
	for k := range refs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var errs []error

	for _, key := range keys {
		value := refs[key]

		if !IsSecretReference(value) {
			result[key] = value
			continue
		}

		resolved, err := r.Resolve(ctx, value)
		if err != nil {
			errs = append(errs, fmt.Errorf("key %q: %w", key, err))
			result[key] = value // preserve original reference so callers see all keys
			continue
		}

		result[key] = resolved
	}

	if len(errs) > 0 {
		return result, fmt.Errorf("azdext.KeyVaultResolver.ResolveMap: %w", errors.Join(errs...))
	}

	return result, nil
}

// SecretReference represents a parsed Key Vault secret reference.
// It may be populated from either the akvs:// or @Microsoft.KeyVault format.
type SecretReference struct {
	// SubscriptionID is the Azure subscription containing the Key Vault.
	// Present for akvs:// references; empty for @Microsoft.KeyVault references.
	SubscriptionID string

	// VaultName is the Key Vault name (not the full URL).
	VaultName string

	// SecretName is the name of the secret within the vault.
	SecretName string

	// SecretVersion is the specific secret version to retrieve.
	// Empty string means latest version.
	SecretVersion string

	// VaultURL is the full vault URL (e.g., "https://my-vault.vault.azure.net").
	// Present for @Microsoft.KeyVault references; empty for akvs:// references
	// (where the URL is constructed from VaultName + VaultSuffix).
	VaultURL string
}

// IsSecretReference reports whether s is a Key Vault secret reference
// in either the akvs:// or @Microsoft.KeyVault(SecretUri=...) format.
func IsSecretReference(s string) bool {
	return keyvault.IsSecretReference(s)
}

// vaultNameRe validates Azure Key Vault names per Azure naming rules:
//   - 3–24 characters
//   - starts with a letter
//   - contains only alphanumeric and hyphens
//   - does not end with a hyphen
var vaultNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{1,22}[a-zA-Z0-9]$`)

// ParseSecretReference parses a Key Vault secret reference into its components.
//
// Two formats are supported:
//
//	akvs://<subscription-id>/<vault-name>/<secret-name>
//	@Microsoft.KeyVault(SecretUri=https://<vault>.vault.azure.net/secrets/<secret>[/<version>])
//
// For the akvs:// format, the vault name is validated against Azure Key Vault
// naming rules (3–24 characters, starts with letter, alphanumeric and hyphens
// only, does not end with a hyphen).
func ParseSecretReference(ref string) (*SecretReference, error) {
	if keyvault.IsKeyVaultAppReference(ref) {
		return parseKeyVaultAppReference(ref)
	}

	return parseAkvsReference(ref)
}

// parseAkvsReference parses an akvs:// URI into its components.
func parseAkvsReference(ref string) (*SecretReference, error) {
	parsed, err := keyvault.ParseAzureKeyVaultSecret(ref)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(parsed.SubscriptionId) == "" {
		return nil, fmt.Errorf("invalid akvs:// reference %q: subscription-id must not be empty", ref)
	}
	if strings.TrimSpace(parsed.VaultName) == "" {
		return nil, fmt.Errorf("invalid akvs:// reference %q: vault-name must not be empty", ref)
	}
	if !vaultNameRe.MatchString(parsed.VaultName) {
		return nil, fmt.Errorf(
			"invalid akvs:// reference %q: vault name %q must be 3-24 characters, "+
				"start with a letter, and contain only alphanumeric characters and hyphens",
			ref, parsed.VaultName,
		)
	}
	if strings.TrimSpace(parsed.SecretName) == "" {
		return nil, fmt.Errorf("invalid akvs:// reference %q: secret-name must not be empty", ref)
	}

	return &SecretReference{
		SubscriptionID: parsed.SubscriptionId,
		VaultName:      parsed.VaultName,
		SecretName:     parsed.SecretName,
	}, nil
}

// parseKeyVaultAppReference parses an @Microsoft.KeyVault(SecretUri=...) reference
// by delegating to the core keyvault package.
func parseKeyVaultAppReference(ref string) (*SecretReference, error) {
	parsed, err := keyvault.ParseKeyVaultAppReference(ref)
	if err != nil {
		return nil, err
	}

	return &SecretReference{
		VaultName:     parsed.VaultName,
		SecretName:    parsed.SecretName,
		SecretVersion: parsed.SecretVersion,
		VaultURL:      parsed.VaultURL,
	}, nil
}

// ResolveReason classifies the cause of a [KeyVaultResolveError].
type ResolveReason int

const (
	// ResolveReasonInvalidReference indicates the secret reference is malformed.
	ResolveReasonInvalidReference ResolveReason = iota

	// ResolveReasonClientCreation indicates failure to create the Key Vault client.
	ResolveReasonClientCreation

	// ResolveReasonNotFound indicates the secret does not exist.
	ResolveReasonNotFound

	// ResolveReasonAccessDenied indicates an authentication or authorization failure.
	ResolveReasonAccessDenied

	// ResolveReasonServiceError indicates an unexpected Key Vault service error.
	ResolveReasonServiceError
)

// String returns a human-readable label for the reason.
func (r ResolveReason) String() string {
	switch r {
	case ResolveReasonInvalidReference:
		return "invalid_reference"
	case ResolveReasonClientCreation:
		return "client_creation"
	case ResolveReasonNotFound:
		return "not_found"
	case ResolveReasonAccessDenied:
		return "access_denied"
	case ResolveReasonServiceError:
		return "service_error"
	default:
		return "unknown"
	}
}

// KeyVaultResolveError is returned when [KeyVaultResolver.Resolve] fails.
type KeyVaultResolveError struct {
	// Reference is the original akvs:// URI that was being resolved.
	Reference string

	// Reason classifies the failure.
	Reason ResolveReason

	// Err is the underlying error.
	Err error
}

func (e *KeyVaultResolveError) Error() string {
	return fmt.Sprintf(
		"azdext.KeyVaultResolver: %s (ref=%s): %v",
		e.Reason, e.Reference, e.Err,
	)
}

func (e *KeyVaultResolveError) Unwrap() error {
	return e.Err
}
