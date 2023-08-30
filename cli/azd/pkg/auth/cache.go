// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"unicode"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

// Known entries from msal cache contract. This is not an exhaustive list.
var contractFields = []string{
	"AccessToken",
	"RefreshToken",
	"IdToken",
	"Account",
	"AppMetadata",
}

// The MSAL cache key for the current user. The stored MSAL cached data contains
// all accounts with stored credentials, across all tenants.
// Currently, the underlying MSAL cache data is represented as [Contract] inside the library.
//
// For simplicity in naming the final cached file, which has a unique directory (see [fileCache]),
// and for historical purposes, we use empty string as the key.
//
// It may be tempting to instead use the partition key provided by [cache.ReplaceHints],
// but note that the key is a partitioning key and not a unique user key.
// Also, given that the data contains auth data for all users, we only need a single key
// to store all cached auth information.
const cCurrentUserCacheKey = ""

// msalCacheAdapter adapts our interface to the one expected by cache.ExportReplace.
type msalCacheAdapter struct {
	cache Cache
}

func (a *msalCacheAdapter) Replace(ctx context.Context, cache cache.Unmarshaler, _ cache.ReplaceHints) error {
	val, err := a.cache.Read(cCurrentUserCacheKey)
	if errors.Is(err, errCacheKeyNotFound) {
		return nil
	} else if err != nil {
		return err
	}

	// In msal v1.0, keys were stored with mixed casing; in v1.1., it was changed to lower case.
	// This handles upgrades where we have a v1.0 cache, and we need to convert it to v1.1,
	// by normalizing the appropriate key entries.
	c := map[string]json.RawMessage{}
	if err = json.Unmarshal(val, &c); err == nil {
		for _, contractKey := range contractFields {
			if _, found := c[contractKey]; found {
				msg := []byte(c[contractKey])
				inner := map[string]json.RawMessage{}

				err := json.Unmarshal(msg, &inner)
				if err != nil {
					log.Printf("msal-upgrade: failed to unmarshal inner: %v", err)
					continue
				}

				updated := normalizeKeys(inner)
				if !updated {
					continue
				}

				newMsg, err := json.Marshal(inner)
				if err != nil {
					log.Printf("msal-upgrade: failed to remarshal inner: %v", err)
					continue
				}

				c[contractKey] = json.RawMessage(newMsg)
			}
		}

		if newVal, err := json.Marshal(c); err == nil {
			val = newVal
		} else {
			log.Printf("msal-upgrade: failed to remarshal msal cache: %v", err)
		}
	} else {
		log.Printf("msal-upgrade: failed to unmarshal msal cache: %v", err)
	}

	// Replace the msal cache contents with the new value retrieved.
	if err := cache.Unmarshal(val); err != nil {
		return err
	}
	return nil
}

func (a *msalCacheAdapter) Export(ctx context.Context, cache cache.Marshaler, _ cache.ExportHints) error {
	val, err := cache.Marshal()
	if err != nil {
		return err
	}

	return a.cache.Set(cCurrentUserCacheKey, val)
}

// Normalize keys by removing upper-case keys and replacing them with lower-case keys.
// In the case where a lower-case key and upper-case key exists, the lower-case key entry
// takes precedence.
func normalizeKeys(m map[string]json.RawMessage) (normalized bool) {
	for k, v := range m {
		if hasUpper(k) {
			// An upper-case key entry exists. Delete it as it is no longer allowed.
			delete(m, k)

			// If a lower-case key entry exists, that supersedes it and we are done.
			// Otherwise, we can safely upgrade the cache entry by re-adding it with lower case.
			lower := strings.ToLower(k)
			if _, isLower := m[lower]; !isLower {
				m[lower] = v
			}

			normalized = true
		}
	}

	return normalized
}

func hasUpper(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) && unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

type Cache interface {
	Read(key string) ([]byte, error)
	Set(key string, value []byte) error
}

var errCacheKeyNotFound = errors.New("key not found")
