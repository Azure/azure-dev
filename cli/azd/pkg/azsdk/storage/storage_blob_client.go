// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

// AccountConfig contains the configuration for connecting to a storage account
type AccountConfig struct {
	AccountName    string
	ContainerName  string
	Endpoint       string
	SubscriptionId string
}

var (
	ErrContainerNotFound = errors.New("container not found")

	// ErrConcurrencyConflict is returned when an upload is rejected because the
	// blob was modified after it was loaded (HTTP 412 / ConditionNotMet).
	ErrConcurrencyConflict = errors.New("blob was modified by another process")
)

// ConcurrencyConflictError is returned when a blob write fails an ETag
// precondition (If-Match / If-None-Match).
type ConcurrencyConflictError struct {
	// BlobPath is the blob that could not be written.
	BlobPath string
	// Err is the underlying Azure response error, when available.
	Err error
}

// Error describes the concurrency conflict for BlobPath.
func (e *ConcurrencyConflictError) Error() string {
	return fmt.Sprintf(
		"remote blob '%s' was modified by another process",
		e.BlobPath,
	)
}

// Unwrap returns the underlying error, or ErrConcurrencyConflict.
func (e *ConcurrencyConflictError) Unwrap() error {
	if e.Err != nil {
		return e.Err
	}
	return ErrConcurrencyConflict
}

// Is reports whether target is ErrConcurrencyConflict.
func (e *ConcurrencyConflictError) Is(target error) bool {
	return target == ErrConcurrencyConflict
}

// BlobDownload is the result of downloading a blob, including its ETag for
// optimistic concurrency on a later upload.
type BlobDownload struct {
	// Body is the blob content. The caller must close it.
	Body io.ReadCloser
	// ETag is the blob ETag returned by Azure Blob Storage.
	ETag azcore.ETag
}

type BlobClient interface {
	// Download downloads a blob from the configured storage account container.
	Download(ctx context.Context, blobPath string) (*BlobDownload, error)

	// Upload uploads a blob to the configured storage account container.
	// match is the ETag captured on Download and is sent as If-Match.
	// An empty match sends If-None-Match: * (the blob must not already exist).
	Upload(ctx context.Context, blobPath string, reader io.Reader, match azcore.ETag) (azcore.ETag, error)

	// Delete deletes a blob from the configured storage account container.
	Delete(ctx context.Context, blobPath string) error

	// Items returns a list of blobs in the configured storage account container.
	Items(ctx context.Context) ([]*Blob, error)
}

// NewBlobClient creates a new BlobClient instance to manage blobs within a container.
func NewBlobClient(
	config *AccountConfig,
	client *azblob.Client,
) BlobClient {
	return &blobClient{
		config: config,
		client: client,
	}
}

type blobClient struct {
	config            *AccountConfig
	client            *azblob.Client
	containerVerified bool
	mu                sync.Mutex
}

// Blob represents a blob within a storage account container.
type Blob struct {
	Name         string
	Path         string
	CreationTime time.Time
	LastModified time.Time
}

// Items returns a list of blobs in the configured storage account container.
func (bc *blobClient) Items(ctx context.Context) ([]*Blob, error) {
	if err := bc.ensureContainerReady(ctx); err != nil {
		return nil, err
	}

	blobs, err := bc.listBlobs(ctx)
	if err != nil {
		if bc.isContainerNotFound(err) {
			if createErr := bc.resetAndEnsureContainer(ctx); createErr != nil {
				return nil, createErr
			}
			return bc.listBlobs(ctx)
		}
		return nil, err
	}

	return blobs, nil
}

func (bc *blobClient) listBlobs(ctx context.Context) ([]*Blob, error) {
	blobs := []*Blob{}

	pager := bc.client.NewListBlobsFlatPager(bc.config.ContainerName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get next page of blobs, %w", err)
		}

		for _, blob := range page.Segment.BlobItems {
			blobs = append(blobs, &Blob{
				Name:         filepath.Base(*blob.Name),
				Path:         *blob.Name,
				CreationTime: *blob.Properties.CreationTime,
				LastModified: *blob.Properties.LastModified,
			})
		}
	}

	return blobs, nil
}

// Download downloads a blob from the configured storage account container.
func (bc *blobClient) Download(ctx context.Context, blobPath string) (*BlobDownload, error) {
	if err := bc.ensureContainerReady(ctx); err != nil {
		return nil, err
	}

	resp, err := bc.client.DownloadStream(ctx, bc.config.ContainerName, blobPath, nil)
	if err != nil {
		if bc.isContainerNotFound(err) {
			if createErr := bc.resetAndEnsureContainer(ctx); createErr != nil {
				return nil, createErr
			}
			resp, err = bc.client.DownloadStream(ctx, bc.config.ContainerName, blobPath, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to download blob '%s', %w", blobPath, err)
			}
			return newBlobDownload(resp), nil
		}
		return nil, fmt.Errorf("failed to download blob '%s', %w", blobPath, err)
	}

	return newBlobDownload(resp), nil
}

// Upload uploads a blob to the configured storage account container.
func (bc *blobClient) Upload(
	ctx context.Context, blobPath string, reader io.Reader, match azcore.ETag,
) (azcore.ETag, error) {
	if err := bc.ensureContainerReady(ctx); err != nil {
		return "", err
	}

	opts := blobUploadOptions(match)
	resp, err := bc.client.UploadStream(ctx, bc.config.ContainerName, blobPath, reader, opts)
	if err != nil {
		if bc.isConcurrencyConflict(err) {
			return "", &ConcurrencyConflictError{BlobPath: blobPath, Err: err}
		}
		if bc.isContainerNotFound(err) {
			if createErr := bc.resetAndEnsureContainer(ctx); createErr != nil {
				return "", createErr
			}
			// Only retry if the reader supports seeking back to the start.
			// io.Reader is non-rewindable, so retrying with an exhausted
			// reader would upload empty/partial content.
			if seeker, ok := reader.(io.Seeker); ok {
				if _, seekErr := seeker.Seek(0, io.SeekStart); seekErr == nil {
					resp, err = bc.client.UploadStream(
						ctx, bc.config.ContainerName, blobPath, reader, opts)
					if err != nil {
						if bc.isConcurrencyConflict(err) {
							return "", &ConcurrencyConflictError{BlobPath: blobPath, Err: err}
						}
						return "", fmt.Errorf(
							"failed to upload blob '%s', %w", blobPath, err)
					}
					return etagOrEmpty(resp.ETag), nil
				}
			}
			// Container re-created but can't retry upload; caller must retry
			return "", fmt.Errorf("failed to upload blob '%s', %w", blobPath, err)
		}
		return "", fmt.Errorf("failed to upload blob '%s', %w", blobPath, err)
	}

	return etagOrEmpty(resp.ETag), nil
}

// Delete deletes a blob from the configured storage account container.
func (bc *blobClient) Delete(ctx context.Context, blobPath string) error {
	if err := bc.ensureContainerReady(ctx); err != nil {
		return err
	}

	_, err := bc.client.DeleteBlob(ctx, bc.config.ContainerName, blobPath, nil)
	if err != nil {
		if bc.isContainerNotFound(err) {
			if createErr := bc.resetAndEnsureContainer(ctx); createErr != nil {
				return createErr
			}
			_, err = bc.client.DeleteBlob(ctx, bc.config.ContainerName, blobPath, nil)
			if err != nil {
				return fmt.Errorf("failed to delete blob '%s', %w", blobPath, err)
			}
			return nil
		}
		return fmt.Errorf("failed to delete blob '%s', %w", blobPath, err)
	}

	return nil
}

// ensureContainerReady checks that the container exists on the first call,
// then skips the check on subsequent calls. If a container-not-found error
// occurs during an operation, callers use resetAndEnsureContainer to recover.
func (bc *blobClient) ensureContainerReady(ctx context.Context) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if bc.containerVerified {
		return nil
	}

	if err := bc.ensureContainerExists(ctx); err != nil {
		return err
	}

	bc.containerVerified = true
	return nil
}

// resetAndEnsureContainer resets the verified flag and re-checks/creates the container.
// Used when an operation fails with container-not-found (e.g., container deleted externally).
func (bc *blobClient) resetAndEnsureContainer(ctx context.Context) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	bc.containerVerified = false

	if err := bc.ensureContainerExists(ctx); err != nil {
		return err
	}

	bc.containerVerified = true
	return nil
}

// isContainerNotFound checks if the error indicates the container was not found.
func (bc *blobClient) isContainerNotFound(err error) bool {
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		return respErr.ErrorCode == "ContainerNotFound"
	}
	return false
}

func (bc *blobClient) isConcurrencyConflict(err error) bool {
	respErr, ok := errors.AsType[*azcore.ResponseError](err)
	if !ok {
		return false
	}
	return respErr.StatusCode == http.StatusPreconditionFailed ||
		respErr.ErrorCode == "ConditionNotMet"
}

func blobUploadOptions(match azcore.ETag) *azblob.UploadStreamOptions {
	conditions := &blob.ModifiedAccessConditions{}
	if match == "" {
		conditions.IfNoneMatch = new(azcore.ETagAny)
	} else {
		conditions.IfMatch = new(match)
	}

	return &azblob.UploadStreamOptions{
		AccessConditions: &blob.AccessConditions{
			ModifiedAccessConditions: conditions,
		},
	}
}

func newBlobDownload(resp azblob.DownloadStreamResponse) *BlobDownload {
	return &BlobDownload{
		Body: resp.Body,
		ETag: etagOrEmpty(resp.ETag),
	}
}

func etagOrEmpty(etag *azcore.ETag) azcore.ETag {
	if etag == nil {
		return ""
	}
	return *etag
}

// ensureContainerExists checks if the container exists and creates it if not.
func (bc *blobClient) ensureContainerExists(ctx context.Context) error {
	exists := false

	pager := bc.client.NewListContainersPager(nil)
	for pager.More() && !exists {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed getting next page of containers: %w", err)
		}

		for _, container := range page.ContainerItems {
			if *container.Name == bc.config.ContainerName {
				exists = true
				break
			}
		}
	}

	if !exists {
		_, err := bc.client.CreateContainer(ctx, bc.config.ContainerName, nil)
		if err != nil {
			return fmt.Errorf("failed to create container '%s', %w", bc.config.ContainerName, err)
		}
	}

	return nil
}

// createClient creates a new blob client and caches it for future use
func NewBlobSdkClient(
	credentialProvider auth.MultiTenantCredentialProvider,
	accountConfig *AccountConfig,
	userConfigManager config.UserConfigManager,
	coreClientOptions *azcore.ClientOptions,
	cloud *cloud.Cloud,
	subscriptionResolver account.SubscriptionResolver,
) (*azblob.Client, error) {
	blobOptions := &azblob.ClientOptions{
		ClientOptions: *coreClientOptions,
	}

	if accountConfig.Endpoint == "" {
		accountConfig.Endpoint = cloud.StorageEndpointSuffix
	}

	// Determine if we have a subscriptionId either from the config, or the default subscription
	subscriptionId := accountConfig.SubscriptionId
	if subscriptionId == "" {
		userConfig, err := userConfigManager.Load()
		if err == nil {
			userSubscription, exists := userConfig.GetString("defaults.subscription")
			if exists && userSubscription != "" {
				subscriptionId = userSubscription
			}
		}
	}

	tenantId := ""
	if subscriptionId != "" {
		// If a subscription ID is configured, resolve the subscription for the current account.
		subscription, err := subscriptionResolver.GetSubscription(context.Background(), subscriptionId)
		if err != nil {
			return nil, fmt.Errorf("failed to get subscription '%s': %w", subscriptionId, err)
		}
		tenantId = subscription.UserAccessTenantId
	}

	// Otherwise, use home tenant ID (empty string)

	credential, err := credentialProvider.GetTokenCredential(context.Background(), tenantId)
	if err != nil {
		return nil, err
	}

	serviceUrl := fmt.Sprintf("https://%s.blob.%s", accountConfig.AccountName, accountConfig.Endpoint)
	client, err := azblob.NewClient(serviceUrl, credential, blobOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create blob client, %w", err)
	}

	return client, nil
}
