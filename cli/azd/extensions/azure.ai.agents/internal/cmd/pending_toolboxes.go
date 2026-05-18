// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// pendingToolboxesPath is the UserConfig root for per-endpoint pending toolbox buckets.
const pendingToolboxesPath = configPathPrefix + ".pending-toolboxes"

// PendingToolbox is the per-name record persisted under
// extensions.ai-agents.pending-toolboxes.<endpointHash>.items.<name>.
type PendingToolbox struct {
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"createdAt"`
}

// pendingToolboxBucket is the value persisted per endpoint hash. It carries
// the plain-text endpoint as a sibling of items so the bucket is self-describing.
type pendingToolboxBucket struct {
	Endpoint string                    `json:"endpoint"`
	Items    map[string]PendingToolbox `json:"items,omitempty"`
}

// endpointBucketKey returns the 16-hex-char opaque key used to bucket pending
// records per endpoint. The key shape (hex.EncodeToString of the
// first 8 bytes of the sha256 digest) is part of the persisted config schema:
// changing it would orphan every existing record.
func endpointBucketKey(endpoint string) string {
	normalized := normalizePendingEndpoint(endpoint)
	h := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(h[:8])
}

// normalizePendingEndpoint canonicalizes the endpoint to ensure two equivalent
// endpoints land in the same bucket. Lower-cases the host, strips trailing slashes.
func normalizePendingEndpoint(endpoint string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return strings.ToLower(trimmed)
	}
	u.Host = strings.ToLower(u.Host)
	return strings.TrimRight(u.String(), "/")
}

// pendingBucketPath builds the full UserConfig path for one endpoint bucket.
func pendingBucketPath(endpoint string) string {
	return pendingToolboxesPath + "." + endpointBucketKey(endpoint)
}

// getPendingBucket loads the pending bucket for an endpoint. Returns an empty
// (non-nil) bucket when no record exists.
func getPendingBucket(
	ctx context.Context, azdClient *azdext.AzdClient, endpoint string,
) (*pendingToolboxBucket, error) {
	ch, err := azdext.NewConfigHelper(azdClient)
	if err != nil {
		return nil, fmt.Errorf("pending toolbox bucket: %w", err)
	}

	var bucket pendingToolboxBucket
	found, err := ch.GetUserJSON(ctx, pendingBucketPath(endpoint), &bucket)
	if err != nil {
		return nil, fmt.Errorf("pending toolbox bucket: failed to read: %w", err)
	}

	if !found {
		return &pendingToolboxBucket{
			Endpoint: normalizePendingEndpoint(endpoint),
			Items:    map[string]PendingToolbox{},
		}, nil
	}
	if bucket.Items == nil {
		bucket.Items = map[string]PendingToolbox{}
	}
	if bucket.Endpoint == "" {
		bucket.Endpoint = normalizePendingEndpoint(endpoint)
	}
	return &bucket, nil
}

// setPendingBucket persists a bucket. If the bucket is empty (no items), the
// whole bucket is left in place to preserve the endpoint mapping; callers that
// want full removal should use deletePendingBucket.
func setPendingBucket(
	ctx context.Context, azdClient *azdext.AzdClient, endpoint string, bucket *pendingToolboxBucket,
) error {
	ch, err := azdext.NewConfigHelper(azdClient)
	if err != nil {
		return fmt.Errorf("pending toolbox bucket: %w", err)
	}
	if err := ch.SetUserJSON(ctx, pendingBucketPath(endpoint), bucket); err != nil {
		return fmt.Errorf("pending toolbox bucket: failed to write: %w", err)
	}
	return nil
}

// getPendingToolbox returns the record for a single name, or (nil, nil) when absent.
func getPendingToolbox(
	ctx context.Context, azdClient *azdext.AzdClient, endpoint, name string,
) (*PendingToolbox, error) {
	bucket, err := getPendingBucket(ctx, azdClient, endpoint)
	if err != nil {
		return nil, err
	}
	if v, ok := bucket.Items[name]; ok {
		return &v, nil
	}
	return nil, nil
}

// setPendingToolbox creates or updates a pending record for one toolbox.
func setPendingToolbox(
	ctx context.Context, azdClient *azdext.AzdClient,
	endpoint, name string, record PendingToolbox,
) error {
	bucket, err := getPendingBucket(ctx, azdClient, endpoint)
	if err != nil {
		return err
	}
	if record.CreatedAt == "" {
		record.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	bucket.Items[name] = record
	return setPendingBucket(ctx, azdClient, endpoint, bucket)
}

// clearPendingToolbox removes a single pending record.
// Returns true when an entry existed and was removed.
func clearPendingToolbox(
	ctx context.Context, azdClient *azdext.AzdClient, endpoint, name string,
) (bool, error) {
	bucket, err := getPendingBucket(ctx, azdClient, endpoint)
	if err != nil {
		return false, err
	}
	if _, ok := bucket.Items[name]; !ok {
		return false, nil
	}
	delete(bucket.Items, name)
	return true, setPendingBucket(ctx, azdClient, endpoint, bucket)
}

// listPendingToolboxes returns all pending records for an endpoint.
func listPendingToolboxes(
	ctx context.Context, azdClient *azdext.AzdClient, endpoint string,
) (map[string]PendingToolbox, error) {
	bucket, err := getPendingBucket(ctx, azdClient, endpoint)
	if err != nil {
		return nil, err
	}
	return bucket.Items, nil
}

// pendingToolboxStore is the seam used by commands that need to read or clear
// pending records. The production implementation is azd-host-backed; tests
// substitute an in-memory stub.
type pendingToolboxStore interface {
	// Get returns the pending record for (endpoint, name), or (nil, nil) when
	// absent. A non-nil error means the store could not be consulted at all.
	Get(ctx context.Context, endpoint, name string) (*PendingToolbox, error)
	// Clear removes a single pending record. Reports whether an entry was present.
	Clear(ctx context.Context, endpoint, name string) (bool, error)
}

type azdPendingToolboxStore struct {
	azdClient *azdext.AzdClient
}

func (s *azdPendingToolboxStore) Get(
	ctx context.Context, endpoint, name string,
) (*PendingToolbox, error) {
	return getPendingToolbox(ctx, s.azdClient, endpoint, name)
}

func (s *azdPendingToolboxStore) Clear(
	ctx context.Context, endpoint, name string,
) (bool, error) {
	return clearPendingToolbox(ctx, s.azdClient, endpoint, name)
}

// newAzdPendingToolboxStore opens the production store. The caller must invoke
// the returned closer (via defer) to release the underlying azd client.
func newAzdPendingToolboxStore() (pendingToolboxStore, func(), error) {
	c, err := azdext.NewAzdClient()
	if err != nil {
		return nil, func() {}, err
	}
	closer := func() { c.Close() }
	return &azdPendingToolboxStore{azdClient: c}, closer, nil
}
