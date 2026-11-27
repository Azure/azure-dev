// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package storage

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
)

// StaticWebsiteContainerName is the blob container Azure Storage serves static website content from.
const StaticWebsiteContainerName = "$web"

// StaticWebsiteProperties describes the static website configuration of a storage account blob service.
type StaticWebsiteProperties struct {
	Enabled              bool
	IndexDocument        string
	ErrorDocument404Path string
}

// StaticWebsiteClient manages static website hosting and content for a storage account blob service.
type StaticWebsiteClient struct {
	client *azblob.Client
}

// NewStaticWebsiteClient creates a StaticWebsiteClient for the blob service endpoint of a storage account,
// for example "https://myaccount.blob.core.windows.net/".
func NewStaticWebsiteClient(
	blobEndpoint string,
	credential azcore.TokenCredential,
	clientOptions *azcore.ClientOptions,
) (*StaticWebsiteClient, error) {
	options := &azblob.ClientOptions{}
	if clientOptions != nil {
		options.ClientOptions = *clientOptions
	}

	client, err := azblob.NewClient(blobEndpoint, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating blob client: %w", err)
	}

	return &StaticWebsiteClient{client: client}, nil
}

// GetStaticWebsite returns the static website configuration of the blob service.
func (c *StaticWebsiteClient) GetStaticWebsite(ctx context.Context) (*StaticWebsiteProperties, error) {
	resp, err := c.client.ServiceClient().GetProperties(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("getting blob service properties: %w", err)
	}

	props := &StaticWebsiteProperties{}
	if sw := resp.StaticWebsite; sw != nil {
		props.Enabled = sw.Enabled != nil && *sw.Enabled
		if sw.IndexDocument != nil {
			props.IndexDocument = *sw.IndexDocument
		}
		if sw.ErrorDocument404Path != nil {
			props.ErrorDocument404Path = *sw.ErrorDocument404Path
		}
	}

	return props, nil
}

// EnableStaticWebsite turns on static website hosting. Other blob service properties are left unchanged.
// errorDocument404Path is optional.
func (c *StaticWebsiteClient) EnableStaticWebsite(
	ctx context.Context,
	indexDocument string,
	errorDocument404Path string,
) error {
	staticWebsite := &service.StaticWebsite{
		Enabled:       new(true),
		IndexDocument: new(indexDocument),
	}
	if errorDocument404Path != "" {
		staticWebsite.ErrorDocument404Path = new(errorDocument404Path)
	}

	_, err := c.client.ServiceClient().SetProperties(ctx, &service.SetPropertiesOptions{
		StaticWebsite: staticWebsite,
	})
	if err != nil {
		return fmt.Errorf("enabling static website: %w", err)
	}

	return nil
}

// EnsureWebContainer creates the $web container when it does not exist. Azure Storage creates it when static
// website hosting is enabled, but it can be deleted independently afterwards.
func (c *StaticWebsiteClient) EnsureWebContainer(ctx context.Context) error {
	_, err := c.client.CreateContainer(ctx, StaticWebsiteContainerName, nil)
	if err != nil && !bloberror.HasCode(err, bloberror.ContainerAlreadyExists) {
		return fmt.Errorf("creating container '%s': %w", StaticWebsiteContainerName, err)
	}

	return nil
}

// ListBlobs returns the names of every blob in the $web container.
func (c *StaticWebsiteClient) ListBlobs(ctx context.Context) ([]string, error) {
	names := []string{}
	pager := c.client.NewListBlobsFlatPager(StaticWebsiteContainerName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing blobs in '%s': %w", StaticWebsiteContainerName, err)
		}

		for _, item := range page.Segment.BlobItems {
			if item.Name != nil {
				names = append(names, *item.Name)
			}
		}
	}

	return names, nil
}

// UploadFile uploads a local file into the $web container with the given content type.
func (c *StaticWebsiteClient) UploadFile(
	ctx context.Context,
	blobName string,
	filePath string,
	contentType string,
) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("opening '%s': %w", filePath, err)
	}
	defer file.Close()

	_, err = c.client.UploadFile(ctx, StaticWebsiteContainerName, blobName, file, &azblob.UploadFileOptions{
		HTTPHeaders: &blob.HTTPHeaders{
			BlobContentType: new(contentType),
		},
	})
	if err != nil {
		return fmt.Errorf("uploading '%s': %w", blobName, err)
	}

	return nil
}

// DeleteBlob deletes a blob from the $web container. Deleting a blob that no longer exists is not an error.
func (c *StaticWebsiteClient) DeleteBlob(ctx context.Context, blobName string) error {
	_, err := c.client.DeleteBlob(ctx, StaticWebsiteContainerName, blobName, nil)
	if err != nil && !bloberror.HasCode(err, bloberror.BlobNotFound) {
		return fmt.Errorf("deleting '%s': %w", blobName, err)
	}

	return nil
}

// IsAuthorizationError reports whether err is a blob data-plane authorization failure, which typically means
// the caller is missing a data role such as Storage Blob Data Contributor or is blocked by network rules.
func IsAuthorizationError(err error) bool {
	if bloberror.HasCode(err,
		bloberror.AuthorizationPermissionMismatch,
		bloberror.AuthorizationFailure,
		bloberror.InsufficientAccountPermissions,
	) {
		return true
	}

	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		return respErr.StatusCode == 403
	}

	return false
}
