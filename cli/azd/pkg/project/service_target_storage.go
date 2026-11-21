// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"golang.org/x/sync/errgroup"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk/storage"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

const (
	storageAccountApiVersion = "2023-05-01"

	// Default documents used when azd enables static website hosting on a storage account.
	storageDefaultIndexDocument = "index.html"
	storageDefaultErrorDocument = "404.html"

	storageUploadConcurrency = 8
)

// Storage account kinds that support static website hosting.
var storageStaticWebsiteKinds = []string{"StorageV2", "BlockBlobStorage"}

// storageWebsiteClient is the data-plane surface used by the storage target to manage static website content.
type storageWebsiteClient interface {
	GetStaticWebsite(ctx context.Context) (*storage.StaticWebsiteProperties, error)
	EnableStaticWebsite(ctx context.Context, indexDocument string, errorDocument404Path string) error
	EnsureWebContainer(ctx context.Context) error
	ListBlobs(ctx context.Context) ([]string, error)
	UploadFile(ctx context.Context, blobName string, filePath string, contentType string) error
	DeleteBlob(ctx context.Context, blobName string) error
}

// storageAccountInfo is the subset of the storage account ARM resource used by the storage target.
type storageAccountInfo struct {
	Kind       string `json:"kind"`
	Properties struct {
		PrimaryEndpoints struct {
			Blob string `json:"blob"`
			Web  string `json:"web"`
		} `json:"primaryEndpoints"`
	} `json:"properties"`
}

type storageTarget struct {
	resourceService    *azapi.ResourceService
	credentialProvider account.SubscriptionCredentialProvider
	clientOptions      *azcore.ClientOptions

	// newWebsiteClient creates the data-plane client. Overridable for tests.
	newWebsiteClient func(ctx context.Context, subscriptionId string, blobEndpoint string) (storageWebsiteClient, error)
	// getAccount reads the storage account ARM resource. Overridable for tests.
	getAccount func(ctx context.Context, targetResource *environment.TargetResource) (*storageAccountInfo, error)
}

// NewStorageTarget creates a service target that deploys static content to the static website
// ($web container) of an Azure Storage account.
func NewStorageTarget(
	resourceService *azapi.ResourceService,
	credentialProvider account.SubscriptionCredentialProvider,
	clientOptions *azcore.ClientOptions,
) ServiceTarget {
	st := &storageTarget{
		resourceService:    resourceService,
		credentialProvider: credentialProvider,
		clientOptions:      clientOptions,
	}
	st.newWebsiteClient = st.createWebsiteClient
	st.getAccount = st.getStorageAccount

	return st
}

// RequiredExternalTools returns no tools; uploads use the Azure Storage SDK directly.
func (st *storageTarget) RequiredExternalTools(context.Context, *ServiceConfig) []tools.ExternalTool {
	return nil
}

// Initialize is a no-op for the storage target.
func (st *storageTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Package resolves the directory with the static content to upload.
func (st *storageTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	packagePath := ""
	if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindDirectory)); found {
		packagePath = artifact.Location
	}

	// When the framework did not produce a dedicated output folder, fall back to `dist` from azure.yaml.
	if (packagePath == "" || packagePath == serviceConfig.Path()) && strings.TrimSpace(serviceConfig.OutputPath) != "" {
		packagePath = serviceConfig.OutputPath
	}

	if packagePath == "" {
		packagePath = serviceConfig.Path()
	}

	if !filepath.IsAbs(packagePath) {
		packagePath = filepath.Join(serviceConfig.Path(), packagePath)
	}
	packagePath = filepath.Clean(packagePath)

	// Framework packages for build languages (js, python, dotnet, ...) contain sources, dependencies, server binaries
	// or configuration files unless `dist` narrows them to the static output, so require an explicit `dist`.
	language := serviceConfig.Language
	if strings.TrimSpace(serviceConfig.OutputPath) == "" &&
		language != ServiceLanguageNone && language != ServiceLanguageCustom {
		return nil, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf(
				"no static content output folder configured for service '%s' (language '%s')",
				serviceConfig.Name, language),
			Message: "Without 'dist', azd would publish the full framework package, which can include sources, " +
				"dependencies, server binaries and configuration files, to a publicly readable website.",
			Suggestion: "Set 'dist' in azure.yaml to the folder containing the built static files, " +
				"for example 'dist: build' for JavaScript or 'dist: wwwroot' for .NET.",
			Links: []errorhandler.ErrorLink{{
				URL:   "https://learn.microsoft.com/azure/developer/azure-developer-cli/azd-schema",
				Title: "azure.yaml schema reference",
			}},
		}
	}

	resolvedPath, err := resolveStaticContentDir(serviceConfig, packagePath)
	if err != nil {
		return nil, err
	}
	packagePath = resolvedPath

	return &ServicePackageResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     packagePath,
				LocationKind: LocationKindLocal,
			},
		},
	}, nil
}

// Publish is a no-op for the storage target.
func (st *storageTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
	publishOptions *PublishOptions,
) (*ServicePublishResult, error) {
	return &ServicePublishResult{}, nil
}

// Deploy uploads the packaged static content to the $web container, enabling static website hosting first when
// needed, and removes blobs that are no longer part of the content.
func (st *storageTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	if err := checkResourceType(targetResource, azapi.AzureResourceTypeStorageAccount); err != nil {
		return nil, err
	}

	// The storage target's own package artifact is appended after the framework artifact, so use the last one.
	// The folder is validated again because `--from-package` skips Package.
	artifact, found := serviceContext.Package.FindLast(WithKind(ArtifactKindDirectory))
	if !found || artifact.Location == "" {
		return nil, fmt.Errorf("no static content folder found in package artifacts for service '%s'", serviceConfig.Name)
	}

	contentPath, err := resolveStaticContentDir(serviceConfig, artifact.Location)
	if err != nil {
		return nil, err
	}

	files, err := collectStaticFiles(contentPath)
	if err != nil {
		return nil, err
	}

	progress.SetProgress(NewServiceProgress("Checking storage account"))
	storageAccount, err := st.getAccount(ctx, targetResource)
	if err != nil {
		return nil, err
	}

	supportsWebsite := slices.ContainsFunc(storageStaticWebsiteKinds, func(kind string) bool {
		return strings.EqualFold(kind, storageAccount.Kind)
	})
	if !supportsWebsite {
		return nil, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf(
				"storage account '%s' of kind '%s' does not support static website hosting",
				targetResource.ResourceName(), storageAccount.Kind),
			Message:    "Static websites require a general-purpose v2 (StorageV2) or BlockBlobStorage account.",
			Suggestion: "Set 'kind' to 'StorageV2' for the storage account in your infrastructure and provision again.",
			Links: []errorhandler.ErrorLink{{
				URL:   "https://learn.microsoft.com/azure/storage/blobs/storage-blob-static-website",
				Title: "Static website hosting in Azure Storage",
			}},
		}
	}

	if storageAccount.Properties.PrimaryEndpoints.Blob == "" {
		return nil, fmt.Errorf("storage account '%s' does not expose a blob endpoint", targetResource.ResourceName())
	}

	client, err := st.newWebsiteClient(
		ctx, targetResource.SubscriptionId(), storageAccount.Properties.PrimaryEndpoints.Blob)
	if err != nil {
		return nil, err
	}

	progress.SetProgress(NewServiceProgress("Checking static website configuration"))
	website, err := client.GetStaticWebsite(ctx)
	if err != nil {
		return nil, storageDataPlaneError(targetResource, err)
	}

	if err := client.EnsureWebContainer(ctx); err != nil {
		return nil, storageDataPlaneError(targetResource, err)
	}

	existing, err := client.ListBlobs(ctx)
	if err != nil {
		return nil, storageDataPlaneError(targetResource, err)
	}

	progress.SetProgress(NewServiceProgress(fmt.Sprintf("Uploading %d file(s)", len(files))))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(storageUploadConcurrency)
	for _, blobName := range slices.Sorted(maps.Keys(files)) {
		filePath := files[blobName]
		group.Go(func() error {
			return client.UploadFile(groupCtx, blobName, filePath, staticContentType(blobName))
		})
	}
	if err := group.Wait(); err != nil {
		return nil, storageDataPlaneError(targetResource, err)
	}

	stale := slices.DeleteFunc(existing, func(name string) bool {
		_, keep := files[name]
		return keep
	})
	if len(stale) > 0 {
		progress.SetProgress(NewServiceProgress(fmt.Sprintf("Removing %d stale file(s)", len(stale))))
		group, groupCtx := errgroup.WithContext(ctx)
		group.SetLimit(storageUploadConcurrency)
		for _, blobName := range stale {
			group.Go(func() error {
				return client.DeleteBlob(groupCtx, blobName)
			})
		}
		if err := group.Wait(); err != nil {
			return nil, storageDataPlaneError(targetResource, err)
		}
	}

	// Enable the website only after the content is in place, so blobs that were already in a private $web
	// container are not exposed if the upload or cleanup fails.
	if !website.Enabled {
		progress.SetProgress(NewServiceProgress("Enabling static website"))
		errorDocument := ""
		if _, has := files[storageDefaultErrorDocument]; has {
			errorDocument = storageDefaultErrorDocument
		}
		if err := client.EnableStaticWebsite(ctx, storageDefaultIndexDocument, errorDocument); err != nil {
			return nil, storageDataPlaneError(targetResource, err)
		}
	}

	endpoints := []string{}
	if storageAccount.Properties.PrimaryEndpoints.Web != "" {
		endpoints = append(endpoints, storageAccount.Properties.PrimaryEndpoints.Web)
	}

	artifacts, err := newDeployArtifacts(endpoints, targetResource)
	if err != nil {
		return nil, err
	}

	return &ServiceDeployResult{Artifacts: artifacts}, nil
}

// Endpoints returns the primary static website endpoint of the storage account.
func (st *storageTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	storageAccount, err := st.getAccount(ctx, targetResource)
	if err != nil {
		return nil, err
	}

	if storageAccount.Properties.PrimaryEndpoints.Web == "" {
		return []string{}, nil
	}

	return []string{storageAccount.Properties.PrimaryEndpoints.Web}, nil
}

func (st *storageTarget) getStorageAccount(
	ctx context.Context,
	targetResource *environment.TargetResource,
) (*storageAccountInfo, error) {
	resourceId, err := storageAccountResourceId(targetResource)
	if err != nil {
		return nil, err
	}

	raw, err := st.resourceService.GetRawResource(ctx, *resourceId, storageAccountApiVersion)
	if err != nil {
		return nil, fmt.Errorf("getting storage account '%s': %w", targetResource.ResourceName(), err)
	}

	info := &storageAccountInfo{}
	if err := json.Unmarshal([]byte(raw), info); err != nil {
		return nil, fmt.Errorf("parsing storage account '%s': %w", targetResource.ResourceName(), err)
	}

	return info, nil
}

// storageAccountResourceId parses the account ID so that ResourceID.String() returns the full ID used by ARM.
func storageAccountResourceId(targetResource *environment.TargetResource) (*arm.ResourceID, error) {
	resourceId, err := arm.ParseResourceID(azure.StorageAccountRID(
		targetResource.SubscriptionId(), targetResource.ResourceGroupName(), targetResource.ResourceName()))
	if err != nil {
		return nil, fmt.Errorf("parsing storage account resource ID: %w", err)
	}

	return resourceId, nil
}

func (st *storageTarget) createWebsiteClient(
	ctx context.Context,
	subscriptionId string,
	blobEndpoint string,
) (storageWebsiteClient, error) {
	credential, err := st.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	return storage.NewStaticWebsiteClient(blobEndpoint, credential, st.clientOptions)
}

// storageDataPlaneError adds remediation guidance to blob data-plane authorization failures.
func storageDataPlaneError(targetResource *environment.TargetResource, err error) error {
	if !storage.IsAuthorizationError(err) {
		return fmt.Errorf("deploying to storage account '%s': %w", targetResource.ResourceName(), err)
	}

	return &internal.ErrorWithSuggestion{
		Err: fmt.Errorf("deploying to storage account '%s': %w", targetResource.ResourceName(), err),
		Message: "azd uses your Microsoft Entra identity to manage the static website and upload files. " +
			"This requires management and blob data roles, and network access to the storage account.",
		Suggestion: "Assign the 'Storage Blob Data Contributor' role on the storage account to the deploying " +
			"identity, along with 'Contributor' or 'Storage Account Contributor' to read and enable the static " +
			"website setting. Ensure the storage firewall allows your network, and run 'azd deploy' again. " +
			"Role assignments can take a few minutes to take effect.",
		Links: []errorhandler.ErrorLink{
			{
				URL:   "https://learn.microsoft.com/azure/storage/blobs/assign-azure-role-data-access",
				Title: "Assign an Azure role for access to blob data",
			},
			{
				URL:   "https://learn.microsoft.com/azure/storage/common/storage-network-security",
				Title: "Configure Azure Storage firewalls and virtual networks",
			},
		},
	}
}

// resolveStaticContentDir resolves the static content folder, following symbolic links, and rejects folders that
// would publish the project configuration. Uploading the project root, or any folder that contains it, would publish
// azure.yaml and the .azure folder (which holds environment values) to a publicly readable website.
func resolveStaticContentDir(serviceConfig *ServiceConfig, contentPath string) (string, error) {
	absPath, err := filepath.Abs(contentPath)
	if err != nil {
		return "", fmt.Errorf("resolving static content folder '%s': %w", contentPath, err)
	}

	resolved, err := filepath.EvalSymlinks(absPath)
	if err == nil {
		var info os.FileInfo
		if info, err = os.Stat(resolved); err == nil && !info.IsDir() {
			err = fmt.Errorf("'%s' is not a folder", resolved)
		}
	}
	if err != nil {
		return "", fmt.Errorf(
			"static content folder '%s' for service '%s' does not exist. "+
				"Set 'dist' in azure.yaml to the folder containing the built static files: %w",
			absPath, serviceConfig.Name, err)
	}

	if serviceConfig.Project != nil && serviceConfig.Project.Path != "" {
		projectRoot, err := filepath.Abs(serviceConfig.Project.Path)
		if err != nil {
			return "", fmt.Errorf("resolving project root: %w", err)
		}
		if resolvedRoot, err := filepath.EvalSymlinks(projectRoot); err == nil {
			projectRoot = resolvedRoot
		}

		if isSubPath(projectRoot, resolved) {
			return "", &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"the content folder for service '%s' cannot be the project root or contain it: %s",
					serviceConfig.Name, absPath),
				Message: "Static website content would include azure.yaml and the .azure folder, " +
					"which are publicly readable once uploaded.",
				Suggestion: "Move the service to a subfolder, or set 'dist' in azure.yaml to the folder " +
					"containing the built static files.",
			}
		}
	}

	return resolved, nil
}

// collectStaticFiles maps blob names (forward-slash relative paths) to local file paths for every file under root.
func collectStaticFiles(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return &internal.ErrorWithSuggestion{
				Err:     fmt.Errorf("static content contains a symbolic link: %s", path),
				Message: "azd does not follow symbolic links when uploading static website content.",
				Suggestion: "Replace the symbolic link with the file or folder it points to, " +
					"or exclude it from the build output, and run 'azd deploy' again.",
			}
		}
		if !d.Type().IsRegular() {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = path
		return nil
	})
	if _, isSuggestion := errors.AsType[*internal.ErrorWithSuggestion](err); isSuggestion {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("reading static content from '%s': %w", root, err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("static content folder '%s' is empty", root)
	}

	return files, nil
}

// Content types for common static web assets that are missing from, or inconsistent across, OS MIME tables.
var staticContentTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".htm":         "text/html; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	".txt":         "text/plain; charset=utf-8",
	".xml":         "text/xml; charset=utf-8",
	".svg":         "image/svg+xml",
	".ico":         "image/x-icon",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".webp":        "image/webp",
	".avif":        "image/avif",
	".wasm":        "application/wasm",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ttf":         "font/ttf",
	".otf":         "font/otf",
	".pdf":         "application/pdf",
}

// staticContentType returns the Content-Type for a blob so browsers render it instead of downloading it.
func staticContentType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if contentType, has := staticContentTypes[ext]; has {
		return contentType
	}

	if contentType := mime.TypeByExtension(ext); contentType != "" {
		return contentType
	}

	return "application/octet-stream"
}
