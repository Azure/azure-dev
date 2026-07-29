// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"azureaieval/internal/pkg/evalcore"
)

// CodeDefinitionType is the discriminator the service uses to deserialize a
// code evaluator definition.
//
// The wire shape is snake_case with a lowercase discriminator, matching
// CodeBasedEvaluatorDefinition in the Foundry data-plane OpenAPI document
// (`type` enum ["code"], plus code_text, entry_point, blob_uri, init_parameters,
// data_schema and metrics). An earlier draft documented a camelCase body with
// `type: "CodeBased"`; that shape is not what the deployed service accepts.
const CodeDefinitionType = "code"

// evaluatorTypeCustom marks an evaluator as authored by the project rather
// than shipped by the platform.
const evaluatorTypeCustom = "custom"

// foundryFeaturesHeader opts a request in to preview behaviour. blob_uri and
// entry_point are both declared as preview properties on the code definition,
// so the header is sent with every call that sets one.
const (
	foundryFeaturesHeader  = "Foundry-Features"
	foundryFeatureEvalsV1  = "Evaluations=V1Preview"
	pendingUploadTypeBlob  = "BlobReference"
	defaultCodeMetricName  = "result"
	defaultCodeMetricType  = "continuous"
	defaultMetricDirection = "increase"
	firstEvaluatorVersion  = "1"
	blobTypeHeader         = "x-ms-blob-type"
	blobTypeBlock          = "BlockBlob"
	octetStreamContentType = "application/octet-stream"
)

// DefaultCodeMetrics is used when neither the folder nor the caller declares
// any.
//
// The service rejects a code definition carrying no metrics, and the documented
// evaluator output is a JSON object whose `result` field holds the score, so
// this describes exactly that. It is a default, not a constraint: any declared
// metrics replace it wholesale.
var DefaultCodeMetrics = json.RawMessage(fmt.Sprintf(
	`{%q:{"type":%q,"desirable_direction":%q,"is_primary":true}}`,
	defaultCodeMetricName, defaultCodeMetricType, defaultMetricDirection,
))

// CodeEvaluatorOptions carries the parts of an evaluator version that do not
// come from the Python source itself.
type CodeEvaluatorOptions struct {
	DisplayName    string
	Description    string
	Categories     []string
	InitParameters json.RawMessage
	DataSchema     json.RawMessage
	Metrics        json.RawMessage
}

// codeDefinition is the wire body of a code evaluator definition.
//
// The contract also allows code_text in place of blob_uri, and requires exactly
// one of the two. Only blob_uri is modelled here, because only blob_uri is
// sent; a field that is never populated would suggest a supported alternative
// that has not been exercised.
type codeDefinition struct {
	Type           string          `json:"type"`
	EntryPoint     string          `json:"entry_point,omitempty"`
	BlobURI        string          `json:"blob_uri,omitempty"`
	InitParameters json.RawMessage `json:"init_parameters,omitempty"`
	DataSchema     json.RawMessage `json:"data_schema,omitempty"`
	Metrics        json.RawMessage `json:"metrics,omitempty"`
}

// createEvaluatorVersionRequest is the POST body for a new evaluator version.
// The service assigns the version; it is not carried here.
type createEvaluatorVersionRequest struct {
	Name          string          `json:"name,omitempty"`
	DisplayName   string          `json:"display_name,omitempty"`
	Description   string          `json:"description,omitempty"`
	EvaluatorType string          `json:"evaluator_type,omitempty"`
	Categories    []string        `json:"categories,omitempty"`
	Definition    *codeDefinition `json:"definition"`
}

// pendingUploadRequest starts an upload for one evaluator version.
type pendingUploadRequest struct {
	PendingUploadType string `json:"pendingUploadType"`
}

// PendingUploadResponse is the reply to startPendingUpload: a container to
// write into, and the SAS that authorizes writing.
type PendingUploadResponse struct {
	BlobReference   *BlobReference `json:"blobReference,omitempty"`
	PendingUploadID string         `json:"pendingUploadId,omitempty"`
	Version         string         `json:"version,omitempty"`
}

// BlobReference is a storage location plus the credential to reach it.
type BlobReference struct {
	BlobURI           string          `json:"blobUri,omitempty"`
	StorageAccountARM string          `json:"storageAccountArmId,omitempty"`
	Credential        *BlobCredential `json:"credential,omitempty"`
}

// BlobCredential holds the SAS granted for an upload.
type BlobCredential struct {
	Type   string `json:"type,omitempty"`
	SASUri string `json:"sasUri,omitempty"`
}

// UploadURI returns the container URI carrying the SAS token, or empty when
// the service granted no credential.
func (p *PendingUploadResponse) UploadURI() string {
	if p == nil || p.BlobReference == nil || p.BlobReference.Credential == nil {
		return ""
	}
	return p.BlobReference.Credential.SASUri
}

// ContainerURI returns the container URI without the SAS token. This is what
// the evaluator definition records, because the definition is persisted and a
// SAS in it would expire.
func (p *PendingUploadResponse) ContainerURI() string {
	if p == nil || p.BlobReference == nil {
		return ""
	}
	return p.BlobReference.BlobURI
}

// UploadCodeEvaluatorVersion publishes a folder of Python as a new version of
// a code evaluator.
//
// Every package goes through storage, including a package of one file. The
// contract offers code_text as an alternative and it would save a round trip,
// but nothing observable confirms the executor runs it: the hand-off to the
// evaluation runtime drops both code_text and blob_uri and refetches from the
// catalog, so RAISvc does not reveal which one it prefers. blob_uri is the one
// with a demonstrated consumer, which enumerates the container and reads the
// files back. Choosing the unproven path would trade a saved upload for an
// evaluator that registers cleanly and then fails when it is finally run,
// which is far harder to diagnose than a slow publish. Revisit once the live
// test has actually exercised inline source.
func (c *EvalClient) UploadCodeEvaluatorVersion(
	ctx context.Context,
	pkg *evalcore.CodeEvaluatorPackage,
	opts CodeEvaluatorOptions,
	apiVersion string,
) (*EvaluatorVersion, error) {
	if pkg == nil {
		return nil, fmt.Errorf("no evaluator package to publish")
	}

	definition := &codeDefinition{
		Type:           CodeDefinitionType,
		EntryPoint:     pkg.EntryPoint,
		InitParameters: opts.InitParameters,
		DataSchema:     opts.DataSchema,
		Metrics:        opts.Metrics,
	}
	if len(definition.Metrics) == 0 {
		definition.Metrics = DefaultCodeMetrics
	}

	blobURI, err := c.uploadCodeEvaluatorFiles(ctx, pkg, apiVersion)
	if err != nil {
		return nil, err
	}
	definition.BlobURI = blobURI

	body := &createEvaluatorVersionRequest{
		Name:          pkg.Name,
		DisplayName:   opts.DisplayName,
		Description:   opts.Description,
		EvaluatorType: evaluatorTypeCustom,
		Categories:    opts.Categories,
		Definition:    definition,
	}

	path := pathEvaluators + "/" + url.PathEscape(pkg.Name) + "/versions"
	respBody, err := c.doRequestWithHeaders(
		ctx, http.MethodPost, path, nil, body, apiVersion, previewHeaders(),
	)
	if err != nil {
		return nil, err
	}

	var created EvaluatorVersion
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &created); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
	}
	if created.Name == "" {
		created.Name = pkg.Name
	}
	return &created, nil
}

// uploadCodeEvaluatorFiles writes every file in the package to the container
// the service provisions for the version being created, and returns the
// container URI to record on the definition.
func (c *EvalClient) uploadCodeEvaluatorFiles(
	ctx context.Context,
	pkg *evalcore.CodeEvaluatorPackage,
	apiVersion string,
) (string, error) {
	version := c.NextEvaluatorVersion(ctx, pkg.Name, apiVersion)

	pending, err := c.StartEvaluatorPendingUpload(ctx, pkg.Name, version, apiVersion)
	if err != nil {
		return "", fmt.Errorf("starting the upload for evaluator %q: %w", pkg.Name, err)
	}

	uploadURI := pending.UploadURI()
	if uploadURI == "" {
		return "", fmt.Errorf(
			"the service returned no upload credential for evaluator %q", pkg.Name)
	}
	containerURI := pending.ContainerURI()
	if containerURI == "" {
		return "", fmt.Errorf(
			"the service returned no storage location for evaluator %q", pkg.Name)
	}

	for _, file := range pkg.Files {
		content, err := os.ReadFile(file.AbsPath)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", file.RelPath, err)
		}
		if err := uploadBlob(ctx, uploadURI, file.RelPath, content); err != nil {
			return "", fmt.Errorf("uploading %s: %w", file.RelPath, err)
		}
	}

	return containerURI, nil
}

// StartEvaluatorPendingUpload provisions the storage an evaluator version's
// code is written to.
func (c *EvalClient) StartEvaluatorPendingUpload(
	ctx context.Context,
	name string,
	version string,
	apiVersion string,
) (*PendingUploadResponse, error) {
	path := fmt.Sprintf(
		"%s/%s/versions/%s/startPendingUpload",
		pathEvaluators, url.PathEscape(name), url.PathEscape(version),
	)
	respBody, err := c.doRequestWithHeaders(
		ctx, http.MethodPost, path, nil,
		&pendingUploadRequest{PendingUploadType: pendingUploadTypeBlob},
		apiVersion, previewHeaders(),
	)
	if err != nil {
		return nil, err
	}

	var pending PendingUploadResponse
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &pending); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
	}
	return &pending, nil
}

// NextEvaluatorVersion reports the version the service will assign to the next
// create.
//
// The upload has to name a version before the version exists, because storage
// is provisioned per version while the create that assigns it comes last. The
// service auto-increments, so the next one is the highest registered plus one.
// An unknown evaluator has none, which is version 1.
func (c *EvalClient) NextEvaluatorVersion(
	ctx context.Context,
	name string,
	apiVersion string,
) string {
	list, err := c.ListEvaluatorVersions(ctx, name, apiVersion)
	if err != nil || list == nil || len(list.Value) == 0 {
		return firstEvaluatorVersion
	}
	latest := pickLatestVersion(list.Value)
	number, err := strconv.Atoi(latest)
	if err != nil {
		return firstEvaluatorVersion
	}
	return strconv.Itoa(number + 1)
}

// LatestEvaluatorVersionNumber reports the newest registered version as an
// integer, or 0 when the evaluator is unknown or its versions are not numeric.
func (c *EvalClient) LatestEvaluatorVersionNumber(
	ctx context.Context,
	name string,
	apiVersion string,
) int {
	list, err := c.ListEvaluatorVersions(ctx, name, apiVersion)
	if err != nil || list == nil || len(list.Value) == 0 {
		return 0
	}
	number, err := strconv.Atoi(pickLatestVersion(list.Value))
	if err != nil {
		return 0
	}
	return number
}

// previewHeaders returns the opt-in header for the preview properties the code
// definition relies on.
func previewHeaders() map[string]string {
	return map[string]string{foundryFeaturesHeader: foundryFeatureEvalsV1}
}

// uploadBlob writes one file into a container using a container-level SAS.
//
// A plain client is used rather than the pipeline: the SAS in the URL is the
// credential, and the pipeline's bearer token policy would attach a Foundry
// token to a storage request that has no use for it.
func uploadBlob(ctx context.Context, containerSASUri, blobName string, data []byte) error {
	u, err := url.Parse(containerSASUri)
	if err != nil {
		return fmt.Errorf("invalid container SAS URI: %w", err)
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + blobName

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create upload request: %w", err)
	}
	req.Header.Set(blobTypeHeader, blobTypeBlock)
	req.Header.Set("Content-Type", octetStreamContentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload blob: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		// The body is Azure Storage XML, which says more than the status alone,
		// but it is capped: a rejected upload can answer with a long document.
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("blob upload failed with status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}
