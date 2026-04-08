// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"maps"
	"slices"
	"strconv"
	"sync"
)

const azdDebugMsalCacheEnv = "AZD_DEBUG_MSAL_CACHE"

type msalCacheTracer struct {
	cache   Cache
	enabled bool

	mu     sync.Mutex
	logged map[string]bool
}

type rawMsalCacheContract struct {
	AccessToken  map[string]rawMsalAccessToken  `json:"AccessToken"`
	RefreshToken map[string]rawMsalRefreshToken `json:"RefreshToken"`
	Account      map[string]rawMsalAccount      `json:"Account"`
}

type rawMsalAccessToken struct {
	HomeAccountID string `json:"home_account_id"`
	Realm         string `json:"realm"`
	CachedAt      any    `json:"cached_at"`
	ExpiresOn     any    `json:"expires_on"`
}

type rawMsalRefreshToken struct {
	HomeAccountID string `json:"home_account_id"`
	Environment   string `json:"environment"`
	ClientID      string `json:"client_id"`
	FamilyID      string `json:"family_id"`
	Secret        string `json:"secret"`
}

type rawMsalAccount struct {
	HomeAccountID string `json:"home_account_id"`
	Realm         string `json:"realm"`
	Username      string `json:"username"`
}

func newMsalCacheTracer(cache Cache) *msalCacheTracer {
	return &msalCacheTracer{
		cache:   cache,
		enabled: isMsalCacheTraceEnabled(),
		logged:  map[string]bool{},
	}
}

func isMsalCacheTraceEnabled() bool {
	value := os.Getenv(azdDebugMsalCacheEnv)
	if value == "" {
		return false
	}

	enabled, err := strconv.ParseBool(value)
	return err == nil && enabled
}

func (t *msalCacheTracer) LogSnapshot(phase string) {
	if t == nil || !t.enabled || phase == "" || t.cache == nil {
		return
	}

	t.logSnapshot(phase)
}

func (t *msalCacheTracer) LogSnapshotOnce(phase string) {
	if t == nil || !t.enabled || phase == "" || t.cache == nil {
		return
	}

	t.mu.Lock()
	if t.logged[phase] {
		t.mu.Unlock()
		return
	}
	t.logged[phase] = true
	t.mu.Unlock()

	t.logSnapshot(phase)
}

func (t *msalCacheTracer) logSnapshot(phase string) {
	val, err := t.cache.Read(currentUserCacheKey)
	if errors.Is(err, errCacheKeyNotFound) || len(val) == 0 {
		log.Printf("msal-cache[%s]: refresh_tokens=0 access_tokens=0 accounts=0", phase)
		return
	}
	if err != nil {
		log.Printf("msal-cache[%s]: failed reading cache: %v", phase, err)
		return
	}

	var contract rawMsalCacheContract
	if err := json.Unmarshal(val, &contract); err != nil {
		log.Printf("msal-cache[%s]: failed parsing cache: %v", phase, err)
		return
	}

	log.Printf(
		"msal-cache[%s]: refresh_tokens=%d access_tokens=%d accounts=%d",
		phase,
		len(contract.RefreshToken),
		len(contract.AccessToken),
		len(contract.Account),
	)

	for _, key := range slices.Sorted(maps.Keys(contract.RefreshToken)) {
		rt := contract.RefreshToken[key]
		log.Printf(
			"msal-cache[%s]: refresh_token key_sha256=%s home_account_id=%s "+
				"environment=%s client_id=%s family_id=%s secret_sha256=%s",
			phase,
			shortDigest(key),
			rt.HomeAccountID,
			rt.Environment,
			rt.ClientID,
			rt.FamilyID,
			shortDigest(rt.Secret),
		)
	}

	for _, key := range slices.Sorted(maps.Keys(contract.AccessToken)) {
		at := contract.AccessToken[key]
		log.Printf(
			"msal-cache[%s]: access_token key_sha256=%s home_account_id=%s realm=%s cached_at=%s expires_on=%s",
			phase,
			shortDigest(key),
			at.HomeAccountID,
			at.Realm,
			fmt.Sprint(at.CachedAt),
			fmt.Sprint(at.ExpiresOn),
		)
	}

	for _, key := range slices.Sorted(maps.Keys(contract.Account)) {
		account := contract.Account[key]
		log.Printf(
			"msal-cache[%s]: account key_sha256=%s home_account_id=%s realm=%s username=%s",
			phase,
			shortDigest(key),
			account.HomeAccountID,
			account.Realm,
			account.Username,
		)
	}
}

// shortDigest returns the first 8 hex characters of the SHA-256 digest of s.
func shortDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}
