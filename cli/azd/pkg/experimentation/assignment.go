package experimentation

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// cCacheFileName is the name of the file used to cache assignment information.
const cCacheFileName string = "assign.cache"

// cCacheDirectoryName is the name of the directory created under the user config directory that contains the cache.
const cCacheDirectoryName string = "experimentation"

// MachineIdParameterName is the name of the parameter used to identify the machine ID in the assignment
// request.
const MachineIdParameterName string = "machineid"

// MachineIdParameterName is the name of the parameter used to identify the version of azd in the assignment
// request.
const AzdVersionParameterName string = "azdversion"

// AssignmentsManager manages interaction with the Assignments service, caching the results for 24 hours.
type AssignmentsManager struct {
	cacheRoot string
	client    *tasClient
}

// NewAssignmentsManager creates a new AssignmentManager, which will communicate with the TAS service. The
// AssignmentManager caches the assignment information for 24 hours in files in the user's config directory
// under the "experimentation" subdirectory.
func NewAssignmentsManager(endpoint string, transport policy.Transporter) (*AssignmentsManager, error) {
	configRoot, err := config.GetUserConfigDir()
	if err != nil {
		return nil, err
	}

	cacheRoot := filepath.Join(configRoot, cCacheDirectoryName)
	if err := os.MkdirAll(cacheRoot, osutil.PermissionDirectory); err != nil {
		return nil, err
	}

	client, err := newTasClient(endpoint, &policy.ClientOptions{
		Transport: transport,
	})
	if err != nil {
		return nil, err
	}

	return &AssignmentsManager{
		cacheRoot: cacheRoot,
		client:    client,
	}, nil
}

// Assignment is a subset of the information returned by the TAS service.
type Assignment struct {
	Features          []string
	Flights           map[string]string
	Configs           []AssignmentConfig
	ParameterGroups   []string
	AssignmentContext string
}

// AssignmentConfig is information about a specific config in an assignment.
type AssignmentConfig struct {
	ID         string
	Parameters map[string]interface{}
}

// Assignment gets a the assignment information for this given machine.
//
// When making a request, the current machine ID is passed as a parameter, named "machineid".
func (am *AssignmentsManager) Assignment(ctx context.Context) (*Assignment, error) {
	cachedAssignment, err := am.readResponseFromCache()
	if err != nil {
		log.Printf("could not read assignment from cache: %v", err)
	}

	if cachedAssignment == nil {
		req := &variantAssignmentRequest{
			Parameters: map[string]string{
				MachineIdParameterName:  resource.MachineId(),
				AzdVersionParameterName: internal.VersionInfo().Version.String(),
			},
		}

		assignment, err := am.client.GetVariantAssignments(ctx, req)
		if err != nil {
			return nil, err
		}
		if err := am.cacheResponse(assignment); err != nil {
			log.Printf("failed to cache assignment response: %v", err)
		}

		cachedAssignment = assignment
	}

	var configs []AssignmentConfig

	if cachedAssignment.Configs != nil {
		configs = make([]AssignmentConfig, len(cachedAssignment.Configs))

		for i, config := range cachedAssignment.Configs {
			configs[i] = AssignmentConfig{
				ID:         config.ID,
				Parameters: config.Parameters,
			}
		}
	}

	return &Assignment{
		Features:          cachedAssignment.Features,
		Flights:           cachedAssignment.Flights,
		Configs:           configs,
		ParameterGroups:   cachedAssignment.ParameterGroups,
		AssignmentContext: cachedAssignment.AssignmentContext,
	}, nil
}

// errCacheExpired is returned when the cached data is out of date.
var errCacheExpired = errors.New("cache expired")

// errUnsupportedCacheVersion is returned with the cache version is not supported
var errUnsupportedCacheVersion = errors.New("unsupported cache version")

// cacheResponse caches the response from the TAS service for 24 hours, using the machineId as the cache key.
func (am *AssignmentsManager) cacheResponse(response *treatmentAssignmentResponse) error {
	responseJson, err := json.Marshal(response)
	if err != nil {
		return err
	}

	cacheFile := assignmentCacheFile{
		Version:   1,
		Response:  json.RawMessage(responseJson),
		ExpiresOn: time.Now().UTC().Add(24 * time.Hour),
	}

	cacheJson, err := json.Marshal(cacheFile)
	if err != nil {
		return err
	}

	cacheFilePath := filepath.Join(am.cacheRoot, cCacheFileName)
	return os.WriteFile(cacheFilePath, cacheJson, osutil.PermissionFile)
}

// readResponseFromCache reads the cached response from the TAS service for the given machineId.
func (am *AssignmentsManager) readResponseFromCache() (*treatmentAssignmentResponse, error) {
	cache, err := os.ReadFile(filepath.Join(am.cacheRoot, cCacheFileName))
	if err != nil {
		return nil, err
	}

	var cacheFile assignmentCacheFile
	if err := json.Unmarshal(cache, &cacheFile); err != nil {
		return nil, err
	}

	if cacheFile.Version != 1 {
		return nil, errUnsupportedCacheVersion
	}

	if time.Now().UTC().After(cacheFile.ExpiresOn) {
		return nil, errCacheExpired
	}

	var resp treatmentAssignmentResponse

	if err := json.Unmarshal(cacheFile.Response, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// assignmentCacheFile is the wire format of the cache file we write. The format contains the version (which should
// be incremented if the format changes, and is currently one), the JSON encoded response from the TAS Service and
// an expiration time
type assignmentCacheFile struct {
	Version   int             `json:"version"`
	Response  json.RawMessage `json:"response"`
	ExpiresOn time.Time       `json:"expiresOn"`
}
