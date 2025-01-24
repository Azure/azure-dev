// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"path"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/resources"
)

type Source interface {
	// Name returns the name of the source.
	Name() string
	// ListTemplates returns a list of AZD compatible templates.
	ListExtensions(ctx context.Context) ([]*ExtensionMetadata, error)
	// GetTemplate returns a template by path.
	GetExtension(ctx context.Context, name string) (*ExtensionMetadata, error)
}

type registrySource struct {
	name     string
	registry *Registry
}

// newRegistrySource creates a new registry source.
func newRegistrySource(name string, registry *Registry) (Source, error) {
	if err := validateRegistry(*registry); err != nil {
		return nil, fmt.Errorf("failed to validate registry: %w", err)
	}

	return &registrySource{
		name:     name,
		registry: registry,
	}, nil
}

func (ts *registrySource) Name() string {
	return ts.name
}

// ListTemplates returns a list of templates from the extension source.
func (s *registrySource) ListExtensions(ctx context.Context) ([]*ExtensionMetadata, error) {
	for _, extension := range s.registry.Extensions {
		extension.Source = s.name
	}

	return s.registry.Extensions, nil
}

// GetExtension returns an extension by id.
func (s *registrySource) GetExtension(ctx context.Context, id string) (*ExtensionMetadata, error) {
	allTemplates, err := s.ListExtensions(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed listing templates: %w", err)
	}

	matchingIndex := slices.IndexFunc(allTemplates, func(extension *ExtensionMetadata) bool {
		return strings.EqualFold(extension.Id, id)
	})

	if matchingIndex == -1 {
		return nil, fmt.Errorf("'%s' %w", id, ErrRegistryExtensionNotFound)
	}

	return allTemplates[matchingIndex], nil
}

// validateRegistry validates the registry content and its signature
func validateRegistry(registry Registry) error {
	if registry.Signature == "" {
		log.Println("Registry signature is empty, skipping signature verification")
		return nil
	}

	// Extract the signature
	signature := registry.Signature
	registry.Signature = ""

	// Marshal the remaining registry content back to JSON
	rawRegistry, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal registry content: %w", err)
	}

	publicKeys, err := loadPublicKeys()
	if err != nil {
		return fmt.Errorf("failed to load public keys: %w", err)
	}

	allErrors := []error{}

	for keyName, publicKey := range publicKeys {
		log.Printf("Verifying signature with public key: %s\n", keyName)

		// Verify the signature with the public key
		err = verifySignature(rawRegistry, signature, publicKey)
		if err != nil {
			allErrors = append(allErrors, fmt.Errorf("signature verification failed: %w", err))
			continue
		}

		// Signature verified successfully
		log.Printf("Signature verified successfully with public key: %s\n", keyName)
		return nil
	}

	return fmt.Errorf("signature verification failed with all public keys: %w", errors.Join(allErrors...))
}

func loadPublicKeys() (map[string]*rsa.PublicKey, error) {
	entries, err := resources.PublicKeys.ReadDir("keys")
	if err != nil {
		return nil, fmt.Errorf("failed to read public keys directory: %w", err)
	}

	publicKeys := map[string]*rsa.PublicKey{}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		if !strings.HasSuffix(entry.Name(), ".pem") {
			continue
		}

		publicKeyData, err := resources.PublicKeys.ReadFile(path.Join("keys", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read public key file: %w", err)
		}

		publicKey, err := parsePublicKey(publicKeyData)
		if err != nil {
			return nil, fmt.Errorf("failed to parse public key: %w", err)
		}

		publicKeys[entry.Name()] = publicKey
	}

	return publicKeys, nil
}

// Verify verifies the given data and its Base64-encoded signature
func verifySignature(data []byte, signature string, publicKey *rsa.PublicKey) error {
	// Compute the SHA256 hash of the data
	hash := sha256.Sum256(data)

	// Decode the Base64-encoded signature
	sigBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}

	// Verify the signature with the public key
	err = rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, hash[:], sigBytes)
	if err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}

func sign(data []byte, privateKey *rsa.PrivateKey) (string, error) {
	// Check the key size
	if privateKey.N.BitLen() < 2048 {
		return "", fmt.Errorf("key size is too small, must be at least 2048 bits")
	}

	// Compute the SHA256 hash of the data
	hash := sha256.Sum256(data)

	// Sign the hash with the private key
	signature, err := rsa.SignPKCS1v15(nil, privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("failed to sign data: %w", err)
	}

	// Encode the signature to Base64
	return base64.StdEncoding.EncodeToString(signature), nil
}

// parsePublicKey loads an RSA public key from a PEM file
func parsePublicKey(data []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, fmt.Errorf("invalid public key PEM format")
	}

	publicKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	rsaPubKey, ok := publicKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is not RSA")
	}

	return rsaPubKey, nil
}
