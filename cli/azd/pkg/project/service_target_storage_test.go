// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk/storage"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
)

type fakeStorageWebsiteClient struct {
	mu       sync.Mutex
	website  storage.StaticWebsiteProperties
	enabled  []string
	existing []string
	uploaded map[string]string
	deleted  []string
	getErr   error
	// uploadErr fails every upload when set.
	uploadErr error
	// calls records the order of mutating operations.
	calls []string
}

func (f *fakeStorageWebsiteClient) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeStorageWebsiteClient) GetStaticWebsite(context.Context) (*storage.StaticWebsiteProperties, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &f.website, nil
}

func (f *fakeStorageWebsiteClient) EnableStaticWebsite(_ context.Context, index string, errorDoc string) error {
	f.enabled = []string{index, errorDoc}
	f.record("enable")
	return nil
}

func (f *fakeStorageWebsiteClient) EnsureWebContainer(context.Context) error { return nil }

func (f *fakeStorageWebsiteClient) ListBlobs(context.Context) ([]string, error) {
	return slices.Clone(f.existing), nil
}

func (f *fakeStorageWebsiteClient) UploadFile(_ context.Context, name string, _ string, contentType string) error {
	if f.uploadErr != nil {
		return f.uploadErr
	}
	f.record("upload")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploaded[name] = contentType
	return nil
}

func (f *fakeStorageWebsiteClient) DeleteBlob(_ context.Context, name string) error {
	f.record("delete")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, name)
	return nil
}

func newTestStorageTarget(client *fakeStorageWebsiteClient, kind string) *storageTarget {
	return &storageTarget{
		newWebsiteClient: func(context.Context, string, string) (storageWebsiteClient, error) {
			return client, nil
		},
		getAccount: func(context.Context, *environment.TargetResource) (*storageAccountInfo, error) {
			info := &storageAccountInfo{Kind: kind}
			info.Properties.PrimaryEndpoints.Blob = "https://acct.blob.core.windows.net/"
			info.Properties.PrimaryEndpoints.Web = "https://acct.z13.web.core.windows.net/"
			return info, nil
		},
	}
}

func newTestProgress(t *testing.T) *async.Progress[ServiceProgress] {
	progress := async.NewProgress[ServiceProgress]()
	go func() {
		for range progress.Progress() {
		}
	}()
	t.Cleanup(progress.Done)
	return progress
}

func writeStaticSite(t *testing.T, dir string, files ...string) {
	for _, name := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, []byte(name), 0600))
	}
}

func storageDeployContext(t *testing.T, contentDir string) *ServiceContext {
	serviceContext := NewServiceContext()
	require.NoError(t, serviceContext.Package.Add(&Artifact{
		Kind:         ArtifactKindDirectory,
		Location:     contentDir,
		LocationKind: LocationKindLocal,
	}))
	return serviceContext
}

// resolvedTempDir returns a temp dir with symbolic links resolved (for example /var -> /private/var on macOS), since
// Package returns resolved paths.
func resolvedTempDir(t *testing.T) string {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return dir
}

func storageTargetResource() *environment.TargetResource {
	return environment.NewTargetResource("SUB", "RG", "acct", string(azapi.AzureResourceTypeStorageAccount))
}

func TestStorageTargetPackage(t *testing.T) {
	t.Run("UsesFrameworkOutput", func(t *testing.T) {
		root := resolvedTempDir(t)
		dist := filepath.Join(root, "web", "dist")
		writeStaticSite(t, dist, "index.html")

		serviceConfig := createTestServiceConfig("web", StorageTarget, ServiceLanguageJavaScript)
		serviceConfig.Project.Path = root
		serviceConfig.OutputPath = "dist"

		result, err := (&storageTarget{}).Package(
			t.Context(), serviceConfig, storageDeployContext(t, dist), newTestProgress(t))
		require.NoError(t, err)
		require.Len(t, result.Artifacts, 1)
		require.Equal(t, dist, result.Artifacts[0].Location)
	})

	t.Run("FallsBackToDist", func(t *testing.T) {
		root := resolvedTempDir(t)
		writeStaticSite(t, filepath.Join(root, "web", "public"), "index.html")

		serviceConfig := createTestServiceConfig("web", StorageTarget, ServiceLanguageNone)
		serviceConfig.Project.Path = root
		serviceConfig.OutputPath = "public"

		result, err := (&storageTarget{}).Package(
			t.Context(), serviceConfig, storageDeployContext(t, serviceConfig.Path()), newTestProgress(t))
		require.NoError(t, err)
		require.Equal(t, filepath.Join(root, "web", "public"), result.Artifacts[0].Location)
	})

	t.Run("RejectsProjectRoot", func(t *testing.T) {
		root := t.TempDir()
		serviceConfig := createTestServiceConfig(".", StorageTarget, ServiceLanguageNone)
		serviceConfig.Project.Path = root

		_, err := (&storageTarget{}).Package(
			t.Context(), serviceConfig, storageDeployContext(t, root), newTestProgress(t))
		_, isSuggestion := errors.AsType[*errorhandler.ErrorWithSuggestion](err)
		require.True(t, isSuggestion, "expected ErrorWithSuggestion, got %v", err)
	})

	t.Run("RejectsFolderContainingProjectRoot", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, "app")
		writeStaticSite(t, root, "azure.yaml", "web/index.html")

		serviceConfig := createTestServiceConfig("web", StorageTarget, ServiceLanguageCustom)
		serviceConfig.Project.Path = root
		serviceConfig.OutputPath = filepath.Join("..", "..")

		_, err := (&storageTarget{}).Package(
			t.Context(), serviceConfig, storageDeployContext(t, serviceConfig.Path()), newTestProgress(t))
		_, isSuggestion := errors.AsType[*errorhandler.ErrorWithSuggestion](err)
		require.True(t, isSuggestion, "expected ErrorWithSuggestion, got %v", err)
		require.ErrorContains(t, err, "cannot be the project root or contain it")
	})

	t.Run("RequiresDistForBuildLanguages", func(t *testing.T) {
		root := t.TempDir()
		writeStaticSite(t, filepath.Join(root, "web"), "index.html", ".env")

		serviceConfig := createTestServiceConfig("web", StorageTarget, ServiceLanguageJavaScript)
		serviceConfig.Project.Path = root

		_, err := (&storageTarget{}).Package(
			t.Context(), serviceConfig, storageDeployContext(t, serviceConfig.Path()), newTestProgress(t))
		suggestion, isSuggestion := errors.AsType[*errorhandler.ErrorWithSuggestion](err)
		require.True(t, isSuggestion, "expected ErrorWithSuggestion, got %v", err)
		require.Contains(t, suggestion.Suggestion, "dist")
	})

	t.Run("RequiresDistForDotNetPublishOutput", func(t *testing.T) {
		root := t.TempDir()
		publish := t.TempDir()
		writeStaticSite(t, publish, "web.dll", "appsettings.json", "wwwroot/index.html")

		serviceConfig := createTestServiceConfig("web", StorageTarget, ServiceLanguageDotNet)
		serviceConfig.Project.Path = root

		_, err := (&storageTarget{}).Package(
			t.Context(), serviceConfig, storageDeployContext(t, publish), newTestProgress(t))
		suggestion, isSuggestion := errors.AsType[*errorhandler.ErrorWithSuggestion](err)
		require.True(t, isSuggestion, "expected ErrorWithSuggestion, got %v", err)
		require.Contains(t, suggestion.Suggestion, "wwwroot")
	})

	t.Run("ReturnsResolvedSymlinkedDist", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(t.TempDir(), "build")
		writeStaticSite(t, target, "index.html")
		require.NoError(t, os.MkdirAll(filepath.Join(root, "web"), 0700))
		if err := os.Symlink(target, filepath.Join(root, "web", "dist")); err != nil {
			t.Skipf("symbolic links are not supported: %v", err)
		}

		serviceConfig := createTestServiceConfig("web", StorageTarget, ServiceLanguageNone)
		serviceConfig.Project.Path = root
		serviceConfig.OutputPath = "dist"

		result, err := (&storageTarget{}).Package(
			t.Context(), serviceConfig, storageDeployContext(t, serviceConfig.Path()), newTestProgress(t))
		require.NoError(t, err)
		resolvedTarget, err := filepath.EvalSymlinks(target)
		require.NoError(t, err)
		require.Equal(t, resolvedTarget, result.Artifacts[0].Location)
	})

	t.Run("AllowsServiceFolderWithoutLanguage", func(t *testing.T) {
		root := resolvedTempDir(t)
		writeStaticSite(t, filepath.Join(root, "web"), "index.html")

		serviceConfig := createTestServiceConfig("web", StorageTarget, ServiceLanguageNone)
		serviceConfig.Project.Path = root

		result, err := (&storageTarget{}).Package(
			t.Context(), serviceConfig, storageDeployContext(t, serviceConfig.Path()), newTestProgress(t))
		require.NoError(t, err)
		require.Equal(t, filepath.Join(root, "web"), result.Artifacts[0].Location)
	})

	t.Run("MissingFolder", func(t *testing.T) {
		root := t.TempDir()
		serviceConfig := createTestServiceConfig("web", StorageTarget, ServiceLanguageNone)
		serviceConfig.Project.Path = root
		serviceConfig.OutputPath = "missing"

		_, err := (&storageTarget{}).Package(
			t.Context(), serviceConfig, NewServiceContext(), newTestProgress(t))
		require.ErrorContains(t, err, "does not exist")
	})
}

func TestStorageTargetDeploy(t *testing.T) {
	t.Run("EnablesWebsiteUploadsAndRemovesStaleBlobs", func(t *testing.T) {
		content := t.TempDir()
		writeStaticSite(t, content, "index.html", "404.html", "assets/app.js", "assets/site.css")

		client := &fakeStorageWebsiteClient{
			existing: []string{"index.html", "old.js", "assets/old.css"},
			uploaded: map[string]string{},
		}
		target := newTestStorageTarget(client, "StorageV2")

		result, err := target.Deploy(
			t.Context(),
			createTestServiceConfig("web", StorageTarget, ServiceLanguageNone),
			storageDeployContext(t, content),
			storageTargetResource(),
			newTestProgress(t),
		)
		require.NoError(t, err)

		require.Equal(t, []string{"index.html", "404.html"}, client.enabled)
		require.Equal(t, map[string]string{
			"index.html":      "text/html; charset=utf-8",
			"404.html":        "text/html; charset=utf-8",
			"assets/app.js":   "text/javascript; charset=utf-8",
			"assets/site.css": "text/css; charset=utf-8",
		}, client.uploaded)
		slices.Sort(client.deleted)
		require.Equal(t, []string{"assets/old.css", "old.js"}, client.deleted)

		endpoint, found := result.Artifacts.FindFirst(WithKind(ArtifactKindEndpoint))
		require.True(t, found)
		require.Equal(t, "https://acct.z13.web.core.windows.net/", endpoint.Location)
	})

	t.Run("EnablesWebsiteAfterContentIsInPlace", func(t *testing.T) {
		content := t.TempDir()
		writeStaticSite(t, content, "index.html")

		client := &fakeStorageWebsiteClient{existing: []string{"private.txt"}, uploaded: map[string]string{}}
		_, err := newTestStorageTarget(client, "StorageV2").Deploy(
			t.Context(),
			createTestServiceConfig("web", StorageTarget, ServiceLanguageNone),
			storageDeployContext(t, content),
			storageTargetResource(),
			newTestProgress(t),
		)
		require.NoError(t, err)
		require.Equal(t, []string{"upload", "delete", "enable"}, client.calls)
	})

	t.Run("LeavesWebsiteDisabledWhenUploadFails", func(t *testing.T) {
		content := t.TempDir()
		writeStaticSite(t, content, "index.html")

		client := &fakeStorageWebsiteClient{uploadErr: errors.New("upload failed"), uploaded: map[string]string{}}
		_, err := newTestStorageTarget(client, "StorageV2").Deploy(
			t.Context(),
			createTestServiceConfig("web", StorageTarget, ServiceLanguageNone),
			storageDeployContext(t, content),
			storageTargetResource(),
			newTestProgress(t),
		)
		require.ErrorContains(t, err, "upload failed")
		require.Nil(t, client.enabled)
	})

	t.Run("UsesStorageTargetPackageArtifact", func(t *testing.T) {
		root := t.TempDir()
		content := filepath.Join(root, "web", "dist")
		writeStaticSite(t, content, "index.html")

		serviceConfig := createTestServiceConfig("web", StorageTarget, ServiceLanguageNone)
		serviceConfig.Project.Path = root

		// The framework artifact comes first; the storage target's validated artifact is appended last.
		serviceContext := storageDeployContext(t, root)
		require.NoError(t, serviceContext.Package.Add(&Artifact{
			Kind:         ArtifactKindDirectory,
			Location:     content,
			LocationKind: LocationKindLocal,
		}))

		client := &fakeStorageWebsiteClient{uploaded: map[string]string{}}
		_, err := newTestStorageTarget(client, "StorageV2").Deploy(
			t.Context(), serviceConfig, serviceContext, storageTargetResource(), newTestProgress(t))
		require.NoError(t, err)
		require.Equal(t, map[string]string{"index.html": "text/html; charset=utf-8"}, client.uploaded)
	})

	t.Run("RejectsProjectRootFromPackage", func(t *testing.T) {
		root := t.TempDir()
		writeStaticSite(t, root, "azure.yaml", ".azure/dev/.env")

		serviceConfig := createTestServiceConfig("web", StorageTarget, ServiceLanguageNone)
		serviceConfig.Project.Path = root

		client := &fakeStorageWebsiteClient{uploaded: map[string]string{}}
		_, err := newTestStorageTarget(client, "StorageV2").Deploy(
			t.Context(), serviceConfig, storageDeployContext(t, root), storageTargetResource(), newTestProgress(t))
		require.ErrorContains(t, err, "cannot be the project root or contain it")
		require.Empty(t, client.uploaded)
	})

	t.Run("RejectsSymbolicLinks", func(t *testing.T) {
		content := t.TempDir()
		writeStaticSite(t, content, "index.html")
		if err := os.Symlink(filepath.Join(content, "index.html"), filepath.Join(content, "link.html")); err != nil {
			t.Skipf("symbolic links are not supported: %v", err)
		}

		client := &fakeStorageWebsiteClient{uploaded: map[string]string{}}
		_, err := newTestStorageTarget(client, "StorageV2").Deploy(
			t.Context(),
			createTestServiceConfig("web", StorageTarget, ServiceLanguageNone),
			storageDeployContext(t, content),
			storageTargetResource(),
			newTestProgress(t),
		)
		_, isSuggestion := errors.AsType[*errorhandler.ErrorWithSuggestion](err)
		require.True(t, isSuggestion, "expected ErrorWithSuggestion, got %v", err)
		require.ErrorContains(t, err, "symbolic link")
		require.Empty(t, client.uploaded)
	})

	t.Run("FollowsSymlinkedContentFolder", func(t *testing.T) {
		content := t.TempDir()
		writeStaticSite(t, content, "index.html")
		link := filepath.Join(t.TempDir(), "site")
		if err := os.Symlink(content, link); err != nil {
			t.Skipf("symbolic links are not supported: %v", err)
		}

		client := &fakeStorageWebsiteClient{uploaded: map[string]string{}}
		_, err := newTestStorageTarget(client, "StorageV2").Deploy(
			t.Context(),
			createTestServiceConfig("web", StorageTarget, ServiceLanguageNone),
			storageDeployContext(t, link),
			storageTargetResource(),
			newTestProgress(t),
		)
		require.NoError(t, err)
		require.Contains(t, client.uploaded, "index.html")
	})

	t.Run("KeepsExistingWebsiteConfiguration", func(t *testing.T) {
		content := t.TempDir()
		writeStaticSite(t, content, "index.html")

		client := &fakeStorageWebsiteClient{
			website:  storage.StaticWebsiteProperties{Enabled: true, IndexDocument: "home.html"},
			uploaded: map[string]string{},
		}
		_, err := newTestStorageTarget(client, "StorageV2").Deploy(
			t.Context(),
			createTestServiceConfig("web", StorageTarget, ServiceLanguageNone),
			storageDeployContext(t, content),
			storageTargetResource(),
			newTestProgress(t),
		)
		require.NoError(t, err)
		require.Nil(t, client.enabled)
		require.Empty(t, client.deleted)
	})

	t.Run("UnsupportedAccountKind", func(t *testing.T) {
		content := t.TempDir()
		writeStaticSite(t, content, "index.html")

		_, err := newTestStorageTarget(&fakeStorageWebsiteClient{}, "Storage").Deploy(
			t.Context(),
			createTestServiceConfig("web", StorageTarget, ServiceLanguageNone),
			storageDeployContext(t, content),
			storageTargetResource(),
			newTestProgress(t),
		)
		require.ErrorContains(t, err, "does not support static website hosting")
	})

	t.Run("AuthorizationFailureSuggestsDataRole", func(t *testing.T) {
		content := t.TempDir()
		writeStaticSite(t, content, "index.html")

		client := &fakeStorageWebsiteClient{
			getErr: &azcore.ResponseError{StatusCode: http.StatusForbidden},
		}
		_, err := newTestStorageTarget(client, "StorageV2").Deploy(
			t.Context(),
			createTestServiceConfig("web", StorageTarget, ServiceLanguageNone),
			storageDeployContext(t, content),
			storageTargetResource(),
			newTestProgress(t),
		)
		suggestion, ok := errors.AsType[*errorhandler.ErrorWithSuggestion](err)
		require.True(t, ok, "expected ErrorWithSuggestion, got %v", err)
		require.Contains(t, suggestion.Suggestion, "Storage Blob Data Contributor")
	})

	t.Run("WrongResourceType", func(t *testing.T) {
		_, err := newTestStorageTarget(&fakeStorageWebsiteClient{}, "StorageV2").Deploy(
			t.Context(),
			createTestServiceConfig("web", StorageTarget, ServiceLanguageNone),
			NewServiceContext(),
			environment.NewTargetResource("SUB", "RG", "site", string(azapi.AzureResourceTypeWebSite)),
			newTestProgress(t),
		)
		require.ErrorContains(t, err, "does not match expected resource type")
	})
}

func TestStorageAccountResourceId(t *testing.T) {
	resourceId, err := storageAccountResourceId(storageTargetResource())
	require.NoError(t, err)
	require.Equal(t, "/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.Storage/storageAccounts/acct",
		resourceId.String())
}

func TestStaticContentType(t *testing.T) {
	tests := map[string]string{
		"index.html":           "text/html; charset=utf-8",
		"Assets/LOGO.SVG":      "image/svg+xml",
		"favicon.ico":          "image/x-icon",
		"fonts/a.woff2":        "font/woff2",
		"site.webmanifest":     "application/manifest+json",
		"no-extension":         "application/octet-stream",
		"data.unknown-ext-azd": "application/octet-stream",
	}

	for name, expected := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, expected, staticContentType(name))
		})
	}
}
