// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"azureaidataset/internal/messages"
	"azureaidataset/internal/urlsafe"
	"azureaidataset/internal/version"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

// API path prefix for dataset endpoints.
const pathDatasets = "/datasets"

// DatasetClient provides methods for dataset upload, download, and metadata retrieval.
type DatasetClient struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewDatasetClient creates a new DatasetClient.
func NewDatasetClient(endpoint string, cred azcore.TokenCredential) *DatasetClient {
	userAgent := fmt.Sprintf("azd-ext-azure-ai-dataset/%s", version.Version)

	clientOptions := &policy.ClientOptions{
		Logging: policy.LogOptions{
			AllowedHeaders: []string{"X-Ms-Correlation-Request-Id", "X-Request-Id"},
			IncludeBody:    false,
		},
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(cred, []string{"https://ai.azure.com/.default"}, nil),
			azsdk.NewMsCorrelationPolicy(),
			azsdk.NewUserAgentPolicy(userAgent),
		},
	}

	pipeline := runtime.NewPipeline(
		"azure-ai-datasets",
		"v1.0.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &DatasetClient{
		endpoint: endpoint,
		pipeline: pipeline,
	}
}

// NewDatasetClientFromPipeline creates a DatasetClient with a pre-built pipeline.
// This is intended for tests that need to bypass auth policies.
func NewDatasetClientFromPipeline(endpoint string, pipeline runtime.Pipeline) *DatasetClient {
	return &DatasetClient{
		endpoint: endpoint,
		pipeline: pipeline,
	}
}

// CreateDataset registers a dataset with inline content (upload).
func (c *DatasetClient) CreateDataset(
	ctx context.Context,
	request *CreateDatasetRequest,
	apiVersion string,
) (*Dataset, error) {
	return doRequestTyped[Dataset](c, ctx, http.MethodPost, pathDatasets, nil, request, apiVersion)
}

// UploadNextVersion registers the next version of a dataset, discovering the
// current one from the service when currentVersion is empty.
//
// Prefer this over UploadNewVersion. That function derives the next version
// from whatever it is handed, so an empty value restarts at 1.0 and the
// service rejects the pending upload with a 409
// TemporaryDataReferencesForExistingAsset as soon as 1.0 exists. Callers
// almost always mean "the version after whatever is registered", which is what
// this does.
//
// The version listing is eventually consistent — it returns nothing for a
// second or two after a version is created — so an empty listing cannot be
// trusted to mean the dataset is new. A conflict is therefore treated as a
// stale read: the listing is re-read, and when it is still behind, the version
// just refused is taken as proof that it exists and the next one is tried.
// Trusting the listing alone left a second upload issued moments after the
// first reporting a 409 to the user for a publish that should simply have
// added a version.
func (c *DatasetClient) UploadNextVersion(
	ctx context.Context,
	name string,
	currentVersion string,
	localDir string,
	apiVersion string,
) (*Dataset, error) {
	if currentVersion == "" {
		latest, err := c.latestRegisteredVersion(ctx, name, apiVersion)
		if err != nil {
			return nil, err
		}
		currentVersion = latest
	}

	var err error
	for range versionConflictAttempts {
		var ds *Dataset
		ds, err = c.UploadNewVersion(ctx, name, currentVersion, localDir, apiVersion)
		if err == nil || !IsVersionConflict(err) {
			return ds, err
		}

		// The version derived from currentVersion is taken, so it exists
		// whatever the listing says. Prefer the listing when it has caught up
		// and moved further ahead; otherwise step past what was just refused.
		refused := NextVersion(currentVersion)
		currentVersion = refused
		// A listing failure is not fatal here: the refused version is already a
		// correct next step, so only a listing that has moved further ahead
		// changes the outcome.
		latest, listErr := c.latestRegisteredVersion(ctx, name, apiVersion)
		if listErr == nil && versionAtLeast(latest, refused) {
			currentVersion = latest
		}
	}
	return nil, err
}

// versionConflictAttempts bounds the walk past versions the listing has not
// caught up with. Each attempt is one refused pending upload, so this is short.
const versionConflictAttempts = 4

// versionAtLeast reports whether a is a version at or beyond b.
func versionAtLeast(a, b string) bool {
	if a == "" {
		return false
	}
	return LatestVersion([]Dataset{{Version: a}, {Version: b}}) == a
}

// latestRegisteredVersion returns the newest registered version. A dataset the
// service does not know, and a listing that has not caught up, both report an
// empty version and no error. Every other failure is returned: treating a 403
// or a timeout as "no versions" would restart an existing dataset at 1.0.
func (c *DatasetClient) latestRegisteredVersion(
	ctx context.Context,
	name string,
	apiVersion string,
) (string, error) {
	list, err := c.ListDatasetVersions(ctx, name, apiVersion)
	if err != nil {
		if IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	if list == nil || len(list.Value) == 0 {
		return "", nil
	}
	return LatestVersion(list.Value), nil
}

// isVersionConflict reports whether the service refused the upload because the
// target version already exists.
func IsVersionConflict(err error) bool {
	respErr, ok := errors.AsType[*azcore.ResponseError](err)
	if !ok {
		return false
	}
	return respErr.StatusCode == http.StatusConflict
}

// IsNotFound reports whether the service answered 404.
func IsNotFound(err error) bool {
	respErr, ok := errors.AsType[*azcore.ResponseError](err)
	if !ok {
		return false
	}
	return respErr.StatusCode == http.StatusNotFound
}

// UploadNewVersion reads the first JSONL file from localDir, computes the next
// version from currentVersion, and uploads it as a new dataset version using
// the 3-step pending upload flow:
//  1. startPendingUpload → get SAS URI
//  2. Upload blob to SAS URI
//  3. Finalize dataset version with dataUri
func (c *DatasetClient) UploadNewVersion(
	ctx context.Context,
	name string,
	currentVersion string,
	localDir string,
	apiVersion string,
) (*Dataset, error) {
	return c.UploadVersion(ctx, name, NextVersion(currentVersion), localDir, apiVersion)
}

// UploadVersion publishes the dataset at exactly this version.
//
// Separate from UploadNewVersion because its parameter is the version to
// count from, not the one to write: passing "1.0" there publishes 2.0. An
// author who declares a version means that version.
func (c *DatasetClient) UploadVersion(
	ctx context.Context,
	name string,
	version string,
	localDir string,
	apiVersion string,
) (*Dataset, error) {
	content, err := ReadFirstJSONLFile(localDir)
	if err != nil {
		return nil, messages.ReadingDatasetFromDir(localDir, err)
	}

	newVersion := version

	// Step 1: Start pending upload to get a SAS URI.
	pending, err := c.StartPendingUpload(ctx, name, newVersion, apiVersion)
	if err != nil {
		return nil, messages.StartingPendingUpload(err)
	}

	uploadURI := pending.ResolvedUploadURI()
	if uploadURI == "" {
		return nil, messages.NoUploadURI()
	}

	// Step 2: Upload the JSONL file to blob storage.
	blobName := name + ".jsonl"
	if err := c.UploadBlob(ctx, uploadURI, blobName, []byte(content)); err != nil {
		return nil, messages.UploadingBlob(err)
	}

	// Step 3: Finalize the dataset version with the full blob URI.
	dataURI := strings.TrimSuffix(pending.ResolvedBlobURI(), "/") + "/" + blobName
	return c.FinalizeDatasetVersion(ctx, name, newVersion, dataURI, apiVersion)
}

// StartPendingUpload initiates a pending upload for a dataset version.
// Returns the SAS URI and blob reference for uploading data.
func (c *DatasetClient) StartPendingUpload(
	ctx context.Context,
	name string,
	version string,
	apiVersion string,
) (*PendingUploadResponse, error) {
	path := fmt.Sprintf(
		"%s/%s/versions/%s/startPendingUpload",
		pathDatasets, url.PathEscape(name), url.PathEscape(version),
	)
	return doRequestTyped[PendingUploadResponse](c, ctx, http.MethodPost, path, nil, json.RawMessage(`{}`), apiVersion)
}

// blobHTTPClient is the client used for direct blob calls.
//
// Bounded: these bypass the SDK pipeline, so nothing else stops a hung storage
// endpoint from holding the command open until someone kills it. Generous, so
// a large dataset over a slow link still finishes.
var blobHTTPClient = &http.Client{Timeout: 10 * time.Minute}

// UploadBlob uploads data to a container SAS URI as a block blob.
func (c *DatasetClient) UploadBlob(ctx context.Context, containerSASUri, blobName string, data []byte) error {
	u, err := url.Parse(containerSASUri)
	if err != nil {
		return messages.InvalidContainerURI(err)
	}

	// Append blob name to the container path.
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + blobName

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(data))
	if err != nil {
		return messages.CreatingUploadRequest(err)
	}
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := blobHTTPClient.Do(req)
	if err != nil {
		return messages.UploadingBlobFailed(urlsafe.Error(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return messages.BlobUploadStatus(resp.StatusCode, string(body))
	}

	return nil
}

// FinalizeDatasetVersion completes the dataset version after blob upload
// by sending the metadata (name, version, dataUri) to the API.
func (c *DatasetClient) FinalizeDatasetVersion(
	ctx context.Context,
	name string,
	version string,
	dataURI string,
	apiVersion string,
) (*Dataset, error) {
	path := fmt.Sprintf("%s/%s/versions/%s", pathDatasets, url.PathEscape(name), url.PathEscape(version))
	request := &FinalizeDatasetRequest{
		Name:    name,
		Version: version,
		Type:    "uri_file",
		DataURI: dataURI,
	}
	return doRequestTyped[Dataset](c, ctx, http.MethodPut, path, nil, request, apiVersion)
}

// GetDataset retrieves metadata for a dataset by name and version.
func (c *DatasetClient) GetDataset(
	ctx context.Context,
	name string,
	version string,
	apiVersion string,
) (*Dataset, error) {
	path := fmt.Sprintf("%s/%s/versions/%s", pathDatasets, url.PathEscape(name), url.PathEscape(version))
	return doRequestTyped[Dataset](c, ctx, http.MethodGet, path, nil, nil, apiVersion)
}

// GetDatasetCredential retrieves a SAS credential for downloading a dataset from blob storage.
func (c *DatasetClient) GetDatasetCredential(
	ctx context.Context,
	name string,
	version string,
	apiVersion string,
) (*DatasetCredential, error) {
	path := fmt.Sprintf(
		"%s/%s/versions/%s/credentials",
		pathDatasets, url.PathEscape(name), url.PathEscape(version),
	)
	return doRequestTyped[DatasetCredential](c, ctx, http.MethodPost, path, nil, nil, apiVersion)
}

// DownloadDatasetContent fetches a dataset version's content, whether its URI
// names a blob or a container.
//
// The two differ by origin, not by any field: a dataset uploaded through
// startPendingUpload gets a URI ending in the file name, while one produced by
// a generation job gets the container it was written into, with isSingleFile
// true either way. Downloading the container directly returns a 409, so the
// blob inside has to be found first.
//
// A credential is always fetched, because the URI on the dataset carries no
// SAS token and an unauthenticated read fails.
func (c *DatasetClient) DownloadDatasetContent(
	ctx context.Context,
	name string,
	version string,
	apiVersion string,
) ([]byte, error) {
	cred, err := c.GetDatasetCredential(ctx, name, version, apiVersion)
	if err != nil {
		return nil, messages.ReadingDownloadCredentials(name, err)
	}

	sasURI := cred.ResolvedDownloadURI()
	if sasURI == "" {
		return nil, messages.NoDownloadURI(name)
	}

	// A URI whose last path segment carries a file extension is the blob
	// itself; anything else is the container holding it.
	if looksLikeBlobURI(sasURI) {
		data, err := c.DownloadDataset(ctx, sasURI)
		if err == nil {
			return data, nil
		}
		log.Printf("[dataset_api] direct download failed (%v); treating the URI as a container", err)
	}

	names, err := c.ListContainerBlobs(ctx, sasURI)
	if err != nil {
		return nil, messages.ListingDatasetContent(name, err)
	}
	blobName := pickDatasetBlob(names)
	if blobName == "" {
		return nil, messages.DatasetHasNoFile(name)
	}
	return c.DownloadBlob(ctx, sasURI, blobName)
}

// looksLikeBlobURI reports whether the URI's final segment names a file.
func looksLikeBlobURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	last := path.Base(strings.TrimSuffix(u.Path, "/"))
	return path.Ext(last) != ""
}

// pickDatasetBlob chooses the file to read from a container, preferring JSONL
// since that is what an evaluation dataset is.
func pickDatasetBlob(names []string) string {
	for _, n := range names {
		if strings.EqualFold(path.Ext(n), ".jsonl") {
			return n
		}
	}
	for _, n := range names {
		if n != "" && !strings.HasSuffix(n, "/") {
			return n
		}
	}
	return ""
}

// DownloadDataset downloads dataset content from blob storage using a SAS-authenticated URL.
// Returns the raw content as bytes. The downloadURL should be the full URL with SAS token
// (e.g., from DatasetCredential.ResolvedDownloadURI()).
func (c *DatasetClient) DownloadDataset(ctx context.Context, downloadURL string) ([]byte, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, downloadURL)
	if err != nil {
		return nil, messages.CreatingDownloadRequest(err)
	}

	// Use a plain HTTP client for blob downloads — the SAS token in the URL provides
	// authentication, and Azure SDK pipeline policies (bearer token, correlation ID)
	// should not be sent to Azure Blob Storage endpoints.
	resp, err := blobHTTPClient.Do(req.Raw())
	if err != nil {
		return nil, messages.DownloadingDatasetBlob(urlsafe.Error(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, messages.BlobDownloadStatus(resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, messages.ReadingDatasetContent(err)
	}

	log.Printf("[dataset_api] downloaded %d bytes", len(data))
	return data, nil
}

// ListContainerBlobs lists blobs in a container using a container-level SAS URI.
// The containerSASUri should include the SAS token (e.g., from credential.sasUri with sr=c).
// Returns a list of blob names found in the container.
func (c *DatasetClient) ListContainerBlobs(ctx context.Context, containerSASUri string) ([]string, error) {
	// Parse the container URI and append list query parameters.
	u, err := url.Parse(containerSASUri)
	if err != nil {
		return nil, messages.InvalidContainerURI(err)
	}

	// The Blob service answers one page and a NextMarker. Only the marker value
	// comes from the service -- the URL is the one built here -- so this walk
	// carries none of the risk that following a body-supplied link would.
	var names []string
	marker := ""
	for range maxListPages {
		page := *u
		q := page.Query()
		q.Set("restype", "container") // cspell:ignore restype — Azure Storage API query parameter
		q.Set("comp", "list")
		if marker != "" {
			q.Set("marker", marker)
		}
		page.RawQuery = q.Encode()

		log.Printf("[dataset_api] listing blobs: %s", urlsafe.URL(&page))

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, page.String(), nil)
		if err != nil {
			return nil, messages.CreatingListRequest(err)
		}

		pageNames, next, err := c.readBlobPage(req)
		if err != nil {
			return nil, err
		}
		names = append(names, pageNames...)
		if next == "" || next == marker {
			break
		}
		marker = next
	}

	log.Printf("[dataset_api] found %d blobs in container", len(names))
	return names, nil
}

// readBlobPage performs one container listing request.
func (c *DatasetClient) readBlobPage(req *http.Request) ([]string, string, error) {
	//nolint:gosec // the URI is the SAS the dataset service issued for this dataset, not caller input
	resp, err := blobHTTPClient.Do(req)
	if err != nil {
		return nil, "", messages.ListingContainerBlobs(urlsafe.Error(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", messages.ContainerListStatus(resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", messages.ReadingListResponse(err)
	}
	names, next := parseBlobPage(string(body))
	return names, next, nil
}

// DownloadBlob downloads a single blob from a container using the container SAS URI
// and the blob name. Returns the blob content as bytes.
func (c *DatasetClient) DownloadBlob(ctx context.Context, containerSASUri, blobName string) ([]byte, error) {
	u, err := url.Parse(containerSASUri)
	if err != nil {
		return nil, messages.InvalidContainerURI(err)
	}

	// Append blob name to the container path.
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + blobName

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, messages.CreatingBlobDownloadRequest(err)
	}

	resp, err := blobHTTPClient.Do(req)
	if err != nil {
		return nil, messages.DownloadingBlob(urlsafe.Error(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, messages.BlobDownloadStatusFor(resp.StatusCode, blobName)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, messages.ReadingBlobContent(err)
	}

	log.Printf("[dataset_api] downloaded blob %s (%d bytes)", blobName, len(data))
	return data, nil
}

// parseBlobNames extracts blob names from the Azure Blob Storage XML list response
// using proper XML parsing against the EnumerationResults schema.
func parseBlobNames(xmlBody string) []string {
	names, _ := parseBlobPage(xmlBody)
	return names
}

// parseBlobPage extracts one page of blob names and the marker that continues
// the listing. An empty marker means this was the last page.
func parseBlobPage(xmlBody string) ([]string, string) {
	type blob struct {
		Name string `xml:"Name"`
	}
	type blobs struct {
		Blob []blob `xml:"Blob"`
	}
	type enumerationResults struct {
		Blobs      blobs  `xml:"Blobs"`
		NextMarker string `xml:"NextMarker"`
	}

	var result enumerationResults
	if err := xml.Unmarshal([]byte(xmlBody), &result); err != nil {
		return nil, ""
	}

	names := make([]string, 0, len(result.Blobs.Blob))
	for _, b := range result.Blobs.Blob {
		if b.Name != "" {
			names = append(names, b.Name)
		}
	}
	return names, result.NextMarker
}

// doRequest performs an HTTP request against the dataset API and returns the raw response body.
func (c *DatasetClient) doRequest(
	ctx context.Context,
	method string,
	path string,
	query map[string]string,
	body any,
	apiVersion string,
) ([]byte, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, messages.InvalidEndpointURL(err)
	}

	// Callers escape the name and version they interpolate, so the path is set
	// as the raw one. Assigning it to u.Path re-escapes the percent signs, and
	// a dataset named "my dataset" then addresses one named "my%20dataset".
	escapedPath := u.EscapedPath() + path
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return nil, messages.InvalidRequestPath(escapedPath, err)
	}
	u.Path, u.RawPath = decodedPath, escapedPath

	q := u.Query()
	if apiVersion != "" {
		q.Set("api-version", apiVersion)
	}
	for k, v := range query {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()

	req, err := runtime.NewRequest(ctx, method, u.String())
	if err != nil {
		return nil, messages.CreatingRequest(err)
	}

	log.Printf("[dataset_api] %s %s", method, urlsafe.URL(u))

	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, messages.MarshalingRequest(err)
		}
		if err := req.SetBody(streaming.NopCloser(bytes.NewReader(payload)), "application/json"); err != nil {
			return nil, messages.SettingRequestBody(err)
		}
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, messages.RequestFailed(err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, messages.ReadingResponseBody(err)
	}

	log.Printf("[dataset_api] response status: %d", resp.StatusCode)

	// 204 belongs here for the same reason it does in eval_api: a delete that
	// removed the version answers No Content, and rejecting that reports every
	// successful delete as an error.
	if !runtime.HasStatusCode(resp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent) {
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		return nil, messages.ServiceRefused(resp.StatusCode, runtime.NewResponseError(resp))
	}

	return respBody, nil
}

// doRequestTyped performs an HTTP request and unmarshals the response into T.
func doRequestTyped[T any](
	c *DatasetClient,
	ctx context.Context,
	method string,
	path string,
	query map[string]string,
	body any,
	apiVersion string,
) (*T, error) {
	respBody, err := c.doRequest(ctx, method, path, query, body, apiVersion)
	if err != nil {
		return nil, err
	}

	if len(respBody) == 0 {
		return new(T), nil
	}

	var result T
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, messages.ParsingResponse(err)
	}

	return &result, nil
}
