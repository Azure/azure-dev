// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/google/uuid"
	"github.com/psanford/memfs"
)

// ExternalImporter implements the Importer interface by forwarding calls
// over a gRPC message broker to an extension process.
type ExternalImporter struct {
	importerName string
	extension    *extensions.Extension
	broker       *grpcbroker.MessageBroker[azdext.ImporterMessage]
}

// Verify ExternalImporter implements Importer at compile time.
var _ Importer = (*ExternalImporter)(nil)

// NewExternalImporter creates a new external importer that delegates to an extension.
func NewExternalImporter(
	name string,
	extension *extensions.Extension,
	broker *grpcbroker.MessageBroker[azdext.ImporterMessage],
) *ExternalImporter {
	return &ExternalImporter{
		importerName: name,
		extension:    extension,
		broker:       broker,
	}
}

// Name returns the display name of this importer.
func (ei *ExternalImporter) Name() string {
	return ei.importerName
}

// CanImport checks if the extension importer can handle the given service.
func (ei *ExternalImporter) CanImport(ctx context.Context, svcConfig *ServiceConfig) (bool, error) {
	var protoSvcConfig azdext.ServiceConfig
	mapServiceConfigToProto(svcConfig, &protoSvcConfig)

	req := &azdext.ImporterMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ImporterMessage_CanImportRequest{
			CanImportRequest: &azdext.ImporterCanImportRequest{
				ServiceConfig: &protoSvcConfig,
			},
		},
	}

	resp, err := ei.broker.SendAndWait(ctx, req)
	if err != nil {
		return false, err
	}

	canImportResp := resp.GetCanImportResponse()
	if canImportResp == nil {
		return false, errors.New("invalid can import response: missing response")
	}

	return canImportResp.CanImport, nil
}

// Services extracts individual service configurations from the project via the extension.
func (ei *ExternalImporter) Services(
	ctx context.Context,
	projectConfig *ProjectConfig,
	svcConfig *ServiceConfig,
) (map[string]*ServiceConfig, error) {
	var protoSvcConfig azdext.ServiceConfig
	mapServiceConfigToProto(svcConfig, &protoSvcConfig)

	var protoProjectConfig azdext.ProjectConfig
	mapProjectConfigToProto(projectConfig, &protoProjectConfig)

	req := &azdext.ImporterMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ImporterMessage_ServicesRequest{
			ServicesRequest: &azdext.ImporterServicesRequest{
				ProjectConfig: &protoProjectConfig,
				ServiceConfig: &protoSvcConfig,
			},
		},
	}

	resp, err := ei.broker.SendAndWait(ctx, req)
	if err != nil {
		return nil, err
	}

	servicesResp := resp.GetServicesResponse()
	if servicesResp == nil {
		return nil, errors.New("invalid services response: missing response")
	}

	// Convert proto ServiceConfig map back to project ServiceConfig map
	result := make(map[string]*ServiceConfig, len(servicesResp.Services))
	for name, protoSvc := range servicesResp.Services {
		svc := mapProtoToServiceConfig(protoSvc, projectConfig)
		svc.Name = name
		result[name] = svc
	}

	return result, nil
}

// ProjectInfrastructure generates temporary infrastructure for provisioning via the extension.
func (ei *ExternalImporter) ProjectInfrastructure(
	ctx context.Context,
	importerPath string,
) (*Infra, error) {
	protoSvcConfig := &azdext.ServiceConfig{
		RelativePath: importerPath,
	}

	req := &azdext.ImporterMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ImporterMessage_ProjectInfrastructureRequest{
			ProjectInfrastructureRequest: &azdext.ImporterProjectInfrastructureRequest{
				ServiceConfig: protoSvcConfig,
			},
		},
	}

	resp, err := ei.broker.SendAndWait(ctx, req)
	if err != nil {
		return nil, err
	}

	infraResp := resp.GetProjectInfrastructureResponse()
	if infraResp == nil {
		return nil, errors.New("invalid project infrastructure response: missing response")
	}

	// Write generated files to a temp directory
	tmpDir, err := os.MkdirTemp("", "azd-ext-infra")
	if err != nil {
		return nil, fmt.Errorf("creating temporary directory: %w", err)
	}

	for _, file := range infraResp.Files {
		target := filepath.Join(tmpDir, file.Path)
		if err := os.MkdirAll(filepath.Dir(target), osutil.PermissionDirectoryOwnerOnly); err != nil {
			return nil, fmt.Errorf("creating directory for %s: %w", file.Path, err)
		}
		if err := os.WriteFile(target, file.Content, osutil.PermissionFile); err != nil {
			return nil, fmt.Errorf("writing file %s: %w", file.Path, err)
		}
	}

	infraOptions := provisioning.Options{
		Path:   tmpDir,
		Module: "main",
	}
	if infraResp.InfraOptions != nil {
		if infraResp.InfraOptions.Provider != "" {
			infraOptions.Provider = provisioning.ProviderKind(infraResp.InfraOptions.Provider)
		}
		if infraResp.InfraOptions.Module != "" {
			infraOptions.Module = infraResp.InfraOptions.Module
		}
	}

	return &Infra{
		Options:    infraOptions,
		cleanupDir: tmpDir,
	}, nil
}

// GenerateAllInfrastructure generates the complete infrastructure filesystem via the extension.
func (ei *ExternalImporter) GenerateAllInfrastructure(
	ctx context.Context,
	importerPath string,
) (fs.FS, error) {
	protoSvcConfig := &azdext.ServiceConfig{
		RelativePath: importerPath,
	}

	req := &azdext.ImporterMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ImporterMessage_GenerateAllInfrastructureRequest{
			GenerateAllInfrastructureRequest: &azdext.ImporterGenerateAllInfrastructureRequest{
				ServiceConfig: protoSvcConfig,
			},
		},
	}

	resp, err := ei.broker.SendAndWait(ctx, req)
	if err != nil {
		return nil, err
	}

	genResp := resp.GetGenerateAllInfrastructureResponse()
	if genResp == nil {
		return nil, errors.New("invalid generate all infrastructure response: missing response")
	}

	// Reconstruct in-memory filesystem from generated files
	mfs := memfs.New()
	for _, file := range genResp.Files {
		dir := filepath.Dir(file.Path)
		if dir != "." {
			if err := mfs.MkdirAll(dir, osutil.PermissionDirectoryOwnerOnly); err != nil {
				return nil, fmt.Errorf("creating directory %s in memfs: %w", dir, err)
			}
		}
		if err := mfs.WriteFile(file.Path, file.Content, osutil.PermissionFile); err != nil {
			return nil, fmt.Errorf("writing file %s to memfs: %w", file.Path, err)
		}
	}

	return mfs, nil
}

// mapServiceConfigToProto converts a project ServiceConfig to its proto representation.
// It sends the fully resolved path so extensions can access project files.
func mapServiceConfigToProto(svc *ServiceConfig, proto *azdext.ServiceConfig) {
	proto.Name = svc.Name
	proto.Host = string(svc.Host)
	proto.Language = string(svc.Language)

	// Send the fully resolved path so extensions can access project files
	// regardless of their own working directory
	if svc.Project != nil {
		proto.RelativePath = svc.Path()
	} else {
		proto.RelativePath = svc.RelativePath
	}

	if !svc.ResourceName.Empty() {
		noopMapping := func(string) string { return "" }
		proto.ResourceName, _ = svc.ResourceName.Envsubst(noopMapping)
	}
}

// mapProjectConfigToProto converts a project ProjectConfig to its proto representation.
func mapProjectConfigToProto(p *ProjectConfig, proto *azdext.ProjectConfig) {
	proto.Name = p.Name
	proto.Path = p.Path
	if !p.ResourceGroupName.Empty() {
		noopMapping := func(string) string { return "" }
		proto.ResourceGroupName, _ = p.ResourceGroupName.Envsubst(noopMapping)
	}
}

// mapProtoToServiceConfig converts a proto ServiceConfig back to a project ServiceConfig.
func mapProtoToServiceConfig(proto *azdext.ServiceConfig, projectConfig *ProjectConfig) *ServiceConfig {
	svc := &ServiceConfig{
		RelativePath: proto.RelativePath,
		Host:         ServiceTargetKind(proto.Host),
		Language:     ServiceLanguageKind(proto.Language),
		Project:      projectConfig,
	}
	return svc
}
