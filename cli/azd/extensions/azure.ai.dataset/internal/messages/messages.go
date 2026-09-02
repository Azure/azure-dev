// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package messages holds every string this extension shows a user.
//
// One file, so the whole voice of the CLI can be reviewed in one sitting and a
// wording change never has to be hunted through the command tree. The only
// extension package it imports is exterrors, which holds no wording of its own,
// so every other package can use this one.
//
// Conventions, so the set stays consistent:
//
//   - Errors state what went wrong and, where there is one, the way out.
//     Lowercase, no trailing period: azd renders them after "ERROR: ".
//   - A name the user chose is quoted with %q; an identifier the service
//     assigned is not, because it is already unmistakable.
//   - Progress and success lines are sentences with a capital and no period.
//   - A printed line carries its own newlines, so a call site is a bare Fprint.
//   - Nothing here decides *whether* to print. That stays at the call site.
package messages

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"

	"azureaidataset/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// ---------------------------------------------------------------------------
// Datasets
// ---------------------------------------------------------------------------

// AssetAlreadyExists reports `create` asked of a name already in use.
func AssetAlreadyExists(kind, name string) error {
	return fmt.Errorf("%s %q already exists: use `update` to publish a new version", kind, name)
}

// AssetDoesNotExist reports `update` asked of a name nobody registered.
func AssetDoesNotExist(kind, name string) error {
	return fmt.Errorf("%s %q does not exist: use `create` to register it", kind, name)
}

// ReadingFromFile reports a --from-file that would not stat.
//
// A path that is simply absent is reported as absent: the wrapped error is a
// syscall name that says nothing to the person who mistyped it.
func ReadingFromFile(path string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("--from-file %q does not exist", filepath.ToSlash(path))
	}
	return fmt.Errorf("reading --from-file %q: %w", filepath.ToSlash(path), err)
}

// FromFileMustBeJSONL reports a --from-file that is not a dataset.
func FromFileMustBeJSONL(path string) error {
	return fmt.Errorf(
		"--from-file must be a .jsonl file or a directory containing one, got %q",
		filepath.ToSlash(path))
}

// FromFileDirectoryHasNoJSONL reports a directory with nothing to upload.
func FromFileDirectoryHasNoJSONL(dir string) error {
	return fmt.Errorf("no .jsonl file in %q; --from-file needs one to upload", filepath.ToSlash(dir))
}

// FromFileDirectoryIsAmbiguous refuses to guess which dataset was meant.
func FromFileDirectoryIsAmbiguous(dir string, names []string) error {
	return fmt.Errorf(
		"%q holds %d .jsonl files (%s); name the one to upload with --from-file",
		filepath.ToSlash(dir), len(names), strings.Join(names, ", "))
}

// DatasetNotFound reports a name that is not a dataset in this project.
func DatasetNotFound(name string) error {
	return fmt.Errorf(
		"no dataset %q in this project; `azd ai dataset list` shows the ones there are",
		name)
}

// InvalidDatasetName reports a name the service will not accept.
func InvalidDatasetName(name string) error {
	return fmt.Errorf(
		"dataset name %q is invalid: use letters, digits, dashes and underscores, "+
			"up to 255 characters", name)
}

// ReadingDatasetDirectory reports the upload scan failing to read the directory.
func ReadingDatasetDirectory(err error) error {
	return fmt.Errorf("reading directory: %w", err)
}

// DatasetFileHasNoRows reports an empty dataset file, refused before upload.
func DatasetFileHasNoRows(name string) error {
	return fmt.Errorf(
		"dataset file %q has no rows, so there would be nothing to evaluate", name)
}

// JSONLRowInvalid reports a row that is not JSON, named by line.
//
// Refused before upload: the service stores the file whatever is in it, so a
// garbage row registers successfully and only fails much later, in the run that
// scores it, against a line number nobody has any more.
func JSONLRowInvalid(name string, line int, err error) error {
	return fmt.Errorf("dataset file %q line %d is not valid JSON: %w", name, line, err)
}

// JSONLRowEmpty reports a row that parses but carries no fields.
func JSONLRowEmpty(name string, line int) error {
	return fmt.Errorf("dataset file %q line %d is an empty object, so it has nothing to score", name, line)
}

// NoJSONLInDirectory reports an upload directory holding no dataset.
func NoJSONLInDirectory(dir string) error {
	return fmt.Errorf("no .jsonl file found in %s", filepath.ToSlash(dir))
}

// StartingPendingUpload reports the service refusing to open an upload.
func StartingPendingUpload(err error) error {
	return fmt.Errorf("starting pending upload: %w", err)
}

// NoUploadURI reports an accepted upload the service gave nowhere to write to.
func NoUploadURI() error {
	return errors.New("no upload SAS URI returned from startPendingUpload")
}

// NoBlobURI reports an accepted upload the service gave no way to finalize.
//
// Separate from NoUploadURI because they are different fields of the same
// response: the SAS says where to write, the blob URI says what to register,
// and a response can carry one without the other.
func NoBlobURI() error {
	return errors.New("no blob URI returned from startPendingUpload, so there is nothing to register the upload as")
}

// UploadingBlob reports the dataset content failing to upload.
func UploadingBlob(err error) error {
	return fmt.Errorf("uploading blob: %w", err)
}

// RegisteringDataset reports the service refusing to publish the dataset.
func RegisteringDataset(dataset string, err error) error {
	return fmt.Errorf("registering dataset %q: %w", dataset, err)
}

// DatasetRegistered confirms a published dataset version.
func DatasetRegistered(dataset, version string) string {
	return fmt.Sprintf("Registered dataset %s version %s\n", dataset, version)
}

// ListingDatasets reports a failure to list the project's datasets.
func ListingDatasets(err error) error {
	return fmt.Errorf("listing datasets: %w", err)
}

// ListingDatasetVersions reports a failure to list one dataset's versions.
func ListingDatasetVersions(dataset string, err error) error {
	return fmt.Errorf("listing versions of dataset %q: %w", dataset, err)
}

// NoDatasets reports a project with no datasets to list.
func NoDatasets() string {
	return "No datasets found.\n"
}

// NoDatasetVersions reports a name nothing is published under.
//
// Listing a name that does not exist is not an error — a delete is checked for
// idempotence this way — so this has to read as an answer about that name
// rather than as a report about the project, which holds other datasets.
func NoDatasetVersions(dataset string) string {
	return fmt.Sprintf("No versions of dataset %q. Publish one with "+
		"`azd ai dataset create %s --from-file <path>`.\n", dataset, dataset)
}

// ResolvingLatestDatasetVersion reports a failure to find what "latest" means.
func ResolvingLatestDatasetVersion(dataset string, err error) error {
	return fmt.Errorf("resolving the latest version of %q: %w", dataset, err)
}

// DatasetHasNoVersions reports a dataset nothing was ever published under.
func DatasetHasNoVersions(dataset string) error {
	return fmt.Errorf("dataset %q has no versions", dataset)
}

// DatasetVersionNotFoundWithHint reports a dataset version the project does not
// hold, to a reader who may have meant a different one.
//
// Kept apart from DatasetVersionNotFound because the listing only helps someone
// looking for a version; a delete already named the one it meant.
func DatasetVersionNotFoundWithHint(dataset, version string) error {
	return fmt.Errorf(
		"no dataset %q at version %q in this project; "+
			"`azd ai dataset versions list %s` shows the ones there are", dataset, version, dataset)
}

// ReadingDatasetVersion reports one version of a dataset failing to read.
func ReadingDatasetVersion(dataset, version string, err error) error {
	return fmt.Errorf("reading dataset %q version %q: %w", dataset, version, err)
}

// CheckingDataset reports the read that decides whether a name is already
// taken. It is worth its own message because that read is what separates
// `create` from `update`, and a failure answered as "not there" turns a create
// into a silent update.
func CheckingDataset(dataset string, err error) error {
	return fmt.Errorf(
		"checking whether dataset %q already exists: %w", dataset, err)
}

// DatasetVersionNotFound reports a dataset version there is nothing to delete at.
func DatasetVersionNotFound(dataset, version string) error {
	return fmt.Errorf("no dataset %q at version %q in this project", dataset, version)
}

// DeletingDatasetVersion reports the service refusing the delete.
func DeletingDatasetVersion(dataset, version string, err error) error {
	return fmt.Errorf("deleting dataset %q version %q: %w", dataset, version, err)
}

// DatasetDeleted confirms a deleted dataset version.
func DatasetDeleted(dataset, version string) string {
	return fmt.Sprintf("Deleted dataset %s version %s\n", dataset, version)
}

// ConfirmDeleteDataset is the question asked before a version is removed.
func ConfirmDeleteDataset(dataset, version string) string {
	return fmt.Sprintf(
		"Delete dataset %s version %s? This cannot be undone.", dataset, version)
}

// DeleteNeedsForce reports a delete that could not ask, because nobody is
// there to answer.
func DeleteNeedsForce(dataset, version string) error {
	return fmt.Errorf(
		"deleting dataset %s version %s removes it for good, and this command "+
			"cannot ask for confirmation without a terminal. Re-run with --force "+
			"to confirm it in advance",
		dataset, version)
}

// DeleteCancelled reports the author answering no.
//
// A line rather than an error: they were asked, they said no, and nothing was
// deleted. That is the command doing its job, and exiting non-zero for it makes
// a deliberate answer indistinguishable from a failure to anything scripted.
func DeleteCancelled(dataset, version string) string {
	return fmt.Sprintf("Left dataset %s version %s alone.\n", dataset, version)
}

// ReadingDownloadCredentials reports the service refusing to hand out a read URI.
func ReadingDownloadCredentials(dataset string, err error) error {
	return fmt.Errorf("reading download credentials for %q: %w", dataset, err)
}

// NoDownloadURI reports a dataset the service gave nowhere to read from.
func NoDownloadURI(dataset string) error {
	return fmt.Errorf("no download URI returned for dataset %q", dataset)
}

// ListingDatasetContent reports a failure to list what a dataset version holds.
func ListingDatasetContent(dataset string, err error) error {
	return fmt.Errorf("listing the content of dataset %q: %w", dataset, err)
}

// DatasetHasNoFile reports a dataset version with nothing to download.
func DatasetHasNoFile(dataset string) error {
	return fmt.Errorf("dataset %q holds no downloadable file", dataset)
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// ConnectingToAzd reports the azd daemon being unreachable.
func ConnectingToAzd(err error) error {
	return fmt.Errorf("connecting to azd: %w", err)
}

// CreatingCredential reports the Azure credential failing to build.
func CreatingCredential(err error) error {
	return fmt.Errorf("creating Azure credential: %w", err)
}

// ErrNoAzdEnvironment reports that there is no azd environment to persist into.
//
// These commands work standalone against the data plane, so running outside a
// project is ordinary rather than a problem worth reporting.
var ErrNoAzdEnvironment = errors.New("no active azd environment")

// NoAzdEnvironmentToWrite reports a value with nowhere to be remembered.
func NoAzdEnvironmentToWrite(key string) error {
	return fmt.Errorf("%w to write %s into", ErrNoAzdEnvironment, key)
}

// WritingEnvValue reports the azd environment refusing a write.
func WritingEnvValue(key string, err error) error {
	return fmt.Errorf("writing %s to the azd environment: %w", key, err)
}

// EndpointEmpty reports a project endpoint given as blank.
func EndpointEmpty() error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		"project endpoint must not be empty",
		"provide a Foundry project endpoint URL "+
			"(e.g. https://<account>.services.ai.azure.com/api/projects/<project>)",
	)
}

// EndpointUnparseable reports a project endpoint that is not a URL.
func EndpointUnparseable(err error) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf("invalid project endpoint URL: %v", err),
		"provide a valid https:// Foundry project endpoint URL",
	)
}

// EndpointNotHTTPS reports a project endpoint on the wrong scheme.
func EndpointNotHTTPS() error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		"project endpoint must use https",
		"provide an https:// URL",
	)
}

// EndpointInAnotherCloud reports a Foundry endpoint in a cloud this extension
// cannot reach.
//
// Separated from EndpointNotFoundryHost because the two are different problems
// with different answers. A malformed host is something the reader can fix; a
// Government endpoint is correct and simply unsupported, and telling them it is
// "not a recognized Foundry host" sends them to check a URL that is already
// right.
func EndpointInAnotherCloud(host, cloud string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf(
			"project endpoint %q is a Foundry endpoint in %s, which this extension "+
				"does not support yet", host, cloud,
		),
		"use a public Azure Foundry project, or track sovereign cloud support before "+
			"relying on this extension there",
	)
}

// EndpointNotFoundryHost reports a project endpoint pointing somewhere else.
func EndpointNotFoundryHost(host, suffix string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf(
			"project endpoint host %q is not a recognized Foundry host (*%s)",
			host, suffix,
		),
		"the host must end with "+suffix,
	)
}

// EndpointHasPort reports a project endpoint carrying an explicit port.
func EndpointHasPort(host string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf("project endpoint host %q must not include a port", host),
		"remove the explicit port from the URL",
	)
}

// NoEndpoint reports a project endpoint that no source could supply.
func NoEndpoint() error {
	return exterrors.Dependency(
		exterrors.CodeMissingProjectEndpoint,
		"no Foundry project endpoint resolved",
		"persist a workspace default with `azd ai project set <endpoint>`, "+
			"or set FOUNDRY_PROJECT_ENDPOINT (or AZURE_AI_PROJECT_ENDPOINT) "+
			"in the active azd environment, "+
			"or export FOUNDRY_PROJECT_ENDPOINT (or AZURE_AI_PROJECT_ENDPOINT) in your shell",
	)
}

// ProjectContextClient reports the config helper failing to build.
func ProjectContextClient(err error) error {
	return fmt.Errorf("getProjectContext: %w", err)
}

// ProjectContextRead reports the persisted project context failing to read.
func ProjectContextRead(err error) error {
	return fmt.Errorf("getProjectContext: failed to read config: %w", err)
}

// ---------------------------------------------------------------------------
// Output
// ---------------------------------------------------------------------------

// Progress markers from the azd style guide, so the extension's lines sit
// alongside core's without a second vocabulary.
const (
	DoneMark    = "(✓) Done:"    // finished successfully
	SkippedMark = "(-) Skipped:" // intentionally not done, not a failure
	FailedMark  = "(x) Failed:"  // the step did not complete
)

// Warning reports a problem that is not worth failing the command over.
func Warning(err error) string {
	return fmt.Sprintf("warning: %v\n", err)
}

// FlagRequired reports a value the command needs and cannot settle itself.
//
// It used to add "(running with --no-prompt)", which was untrue at every call
// site: none of them prompts, so the parenthetical named a flag the caller had
// not passed and implied that dropping it would make the command ask.
func FlagRequired(name string) error {
	return fmt.Errorf("--%s is required", name)
}

// ReadingPath reports a file or directory that could not be read.
func ReadingPath(path string, err error) error {
	return fmt.Errorf("reading %s: %w", path, err)
}

// ---------------------------------------------------------------------------
// Talking to the service
// ---------------------------------------------------------------------------

// InvalidEndpointURL reports a client built on an endpoint that will not parse.
func InvalidEndpointURL(err error) error {
	return fmt.Errorf("invalid endpoint URL: %w", err)
}

// InvalidRequestPath reports a request path that will not parse.
func InvalidRequestPath(path string, err error) error {
	return fmt.Errorf("invalid request path %q: %w", path, err)
}

// InvalidNextLink reports a pagination link the service sent that will not parse.
func InvalidNextLink(link string, err error) error {
	return fmt.Errorf("invalid nextLink %q: %w", link, err)
}

// NextLinkOffOrigin reports a pagination link pointing somewhere other than the
// project endpoint. Following it would send the caller's token to that host.
func NextLinkOffOrigin(origin string) error {
	return fmt.Errorf("refusing to follow nextLink to %s: it is not the project endpoint", origin)
}

// CreatingRequest reports a request that could not be built.
func CreatingRequest(err error) error {
	return fmt.Errorf("failed to create request: %w", err)
}

// MarshalingRequest reports a request body that would not serialize.
func MarshalingRequest(err error) error {
	return fmt.Errorf("failed to marshal request: %w", err)
}

// SettingRequestBody reports a request body that would not attach.
func SettingRequestBody(err error) error {
	return fmt.Errorf("failed to set request body: %w", err)
}

// RequestFailed reports a request that never reached an answer.
//
// A credential that cannot mint a token fails here rather than as a 401, and
// the SDK's own text for it names neither azd nor the way out. isCredentialFailure
// decides which is which; see it for how.
//
// The hint is in the message as well as the suggestion because the suggestion
// is not rendered on every surface, and it offers a retry first: this call
// shells out to `azd auth token`, which has been seen to fail transiently
// against a login that was perfectly valid -- measured once at over 70 seconds,
// long enough to lose to a deadline.
func RequestFailed(err error) error {
	if isCredentialUnavailable(err) {
		// Not an expired login, and `azd auth login` cannot be run to fix it.
		return exterrors.Auth(
			exterrors.CodeAuthFailed,
			fmt.Sprintf(
				"could not get a token for the Foundry project because azd itself "+
					"could not be run: %v", err),
			"check that `azd` is installed and on PATH")
	}
	if isCredentialFailure(err) {
		return exterrors.Auth(
			exterrors.CodeLoginExpired,
			fmt.Sprintf(
				"could not get a token for the Foundry project: %v. "+
					"Try again; if it keeps failing, run `azd auth login`", err),
			"try the command again, then `azd auth login` if it keeps failing")
	}
	return fmt.Errorf("HTTP request failed: %w", err)
}

// isCredentialUnavailable reports the credential never having run at all, as
// opposed to running and being refused.
//
// azidentity's credentialUnavailableError is unexported, so this matches the
// two messages it carries for that case. Worth separating because the answer
// to both is not `azd auth login` -- you cannot log in with a tool that is not
// on PATH.
func isCredentialUnavailable(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "executable not found on path") ||
		strings.Contains(text, "is not recognized")
}

// ServiceRefused turns an unauthorized answer into one that says what to do.
// Every other status is left as the service reported it.
func ServiceRefused(status int, err error) error {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return exterrors.Auth(
			exterrors.CodeAuthFailed,
			fmt.Sprintf(
				"the Foundry project refused the request (HTTP %d): %v. "+
					"Run `azd auth login`, and check you have access to this project",
				status, err),
			"run `azd auth login`, and check you have access to this project")
	}
	return err
}

// isCredentialFailure reports whether the request failed because no token could
// be minted, rather than for any of the other reasons a request fails.
//
// Decided on the SDK's own error types. This used to also match the phrase
// "failed to acquire a token" anywhere in the text, which any error is free to
// contain -- a service that could not acquire a token bucket lease was told its
// login had expired and to run `azd auth login`.
//
// The credential names stay as a fallback because credentialUnavailableError is
// unexported: a credential that never ran can only be recognized by the name it
// puts in its own message.
func isCredentialFailure(err error) bool {
	if err == nil {
		return false
	}

	if _, ok := errors.AsType[*azidentity.AuthenticationFailedError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*azidentity.AuthenticationRequiredError](err); ok {
		return true
	}

	text := err.Error()
	for _, credential := range []string{
		"AzureDeveloperCLICredential",
		"DefaultAzureCredential",
	} {
		if strings.Contains(text, credential) {
			return true
		}
	}
	return false
}

// ReadingResponseBody reports a response that could not be read.
func ReadingResponseBody(err error) error {
	return fmt.Errorf("failed to read response body: %w", err)
}

// ParsingResponse reports a response that could not be parsed.
func ParsingResponse(err error) error {
	return fmt.Errorf("failed to parse response: %w", err)
}

// InvalidContainerURI reports a storage URI the service handed back unusable.
func InvalidContainerURI(err error) error {
	return fmt.Errorf("invalid container SAS URI: %w", err)
}

// CreatingUploadRequest reports the blob upload request failing to build.
func CreatingUploadRequest(err error) error {
	return fmt.Errorf("failed to create upload request: %w", err)
}

// UploadingBlobFailed reports the blob upload never reaching an answer.
func UploadingBlobFailed(err error) error {
	return fmt.Errorf("failed to upload blob: %w", err)
}

// BlobUploadStatus reports storage refusing the upload.
func BlobUploadStatus(status int, body string) error {
	return fmt.Errorf("blob upload failed with status %d: %s", status, body)
}

// CreatingDownloadRequest reports the dataset download request failing to build.
func CreatingDownloadRequest(err error) error {
	return fmt.Errorf("failed to create download request: %w", err)
}

// DownloadingDatasetBlob reports the dataset download never reaching an answer.
func DownloadingDatasetBlob(err error) error {
	return fmt.Errorf("failed to download dataset from blob: %w", err)
}

// BlobDownloadStatus reports storage refusing the download.
func BlobDownloadStatus(status int) error {
	return fmt.Errorf("blob download failed with status %d", status)
}

// ReadingDatasetContent reports a downloaded dataset that could not be read.
func ReadingDatasetContent(err error) error {
	return fmt.Errorf("failed to read dataset content: %w", err)
}

// CreatingListRequest reports the container listing request failing to build.
func CreatingListRequest(err error) error {
	return fmt.Errorf("failed to create list request: %w", err)
}

// ListingContainerBlobs reports the container listing never reaching an answer.
func ListingContainerBlobs(err error) error {
	return fmt.Errorf("failed to list container blobs: %w", err)
}

// ContainerListStatus reports storage refusing the listing.
func ContainerListStatus(status int) error {
	return fmt.Errorf("container list failed with status %d", status)
}

// ReadingListResponse reports a container listing that could not be read.
func ReadingListResponse(err error) error {
	return fmt.Errorf("failed to read list response: %w", err)
}

// ParsingListResponse reports a container listing that did not decode.
func ParsingListResponse(err error) error {
	return fmt.Errorf("failed to parse list response: %w", err)
}

// CreatingBlobDownloadRequest reports the blob download request failing to build.
func CreatingBlobDownloadRequest(err error) error {
	return fmt.Errorf("failed to create blob download request: %w", err)
}

// DownloadingBlob reports one blob's download never reaching an answer.
func DownloadingBlob(err error) error {
	return fmt.Errorf("failed to download blob: %w", err)
}

// BlobDownloadStatusFor reports storage refusing one named blob.
func BlobDownloadStatusFor(status int, blobName string) error {
	return fmt.Errorf("blob download failed with status %d for %s", status, blobName)
}

// ReadingBlobContent reports a downloaded blob that could not be read.
func ReadingBlobContent(err error) error {
	return fmt.Errorf("failed to read blob content: %w", err)
}

// ListingTruncated reports a page walk that stopped before the end.
//
// Worth saying out loud rather than logging: a short listing is indistinguishable
// from a complete one, and log goes to io.Discard unless --debug.
func ListingTruncated(pages int) error {
	return fmt.Errorf(
		"stopped reading the listing after %d pages, so it may be incomplete", pages)
}
