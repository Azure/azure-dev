// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package testutil

import (
	"archive/zip"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"azurespringboot/internal/springmodel"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	coreproject "github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

// TestSpringClientID is a canonical non-secret UUID fixture used across unit tests.
const TestSpringClientID = "11111111-2222-3333-4444-555555555555"

// CreateMavenProject creates a minimal contained H2 Maven Wrapper Spring Boot project in t.TempDir().
func CreateMavenProject(t *testing.T) (*azdext.ProjectConfig, *azdext.ServiceConfig) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".mvn", "wrapper"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o700))
	for _, p := range []string{"pom.xml", "mvnw", filepath.Join(".mvn", "wrapper", "maven-wrapper.properties")} {
		// #nosec G304 -- fixed relative paths inside t.TempDir().
		require.NoError(t, os.WriteFile(filepath.Join(root, p), []byte("fixture"), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project><dependencies><dependency>
<groupId>com.h2database</groupId><artifactId>h2</artifactId><scope>runtime</scope>
</dependency></dependencies></project>`), 0o600))
	service := &azdext.ServiceConfig{Name: "petclinic", Host: springmodel.SpringHost, RelativePath: "."}
	project := &azdext.ProjectConfig{
		Path:     root,
		Services: map[string]*azdext.ServiceConfig{"petclinic": service},
	}
	return project, service
}

// AutomaticPostgresProject creates a minimal Entra-authenticated PostgreSQL Maven Spring Boot project in t.TempDir().
func AutomaticPostgresProject(t *testing.T) (*azdext.ProjectConfig, *azdext.ServiceConfig) {
	t.Helper()
	project, service := CreateMavenProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(project.Path, "pom.xml"), []byte(`<project><dependencies>
<dependency><groupId>org.postgresql</groupId><artifactId>postgresql</artifactId><scope>runtime</scope></dependency>
<dependency><groupId>com.azure</groupId><artifactId>azure-identity-extensions</artifactId><scope>runtime</scope></dependency>
</dependencies></project>`), 0o600))
	resourceDir := filepath.Join(project.Path, "src", "main", "resources")
	require.NoError(t, os.MkdirAll(resourceDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(resourceDir, "application.properties"), []byte(
		"spring.datasource.url=${POSTGRES_URL:jdbc:postgresql://localhost:5432/petclinic}\n"+
			"spring.datasource.username=petclinic_app\n"+
			"spring.datasource.hikari.data-source-properties[authenticationPluginClassName]="+
			springmodel.AzurePostgreSQLPlugin+"\n"+
			"spring.datasource.hikari.data-source-properties[azure.managedIdentityEnabled]=true\n"+
			"spring.datasource.hikari.data-source-properties[azure.clientId]=${AZURE_CLIENT_ID:}\n"+
			"spring.datasource.hikari.data-source-properties[sslmode]=verify-full\n"+
			"spring.datasource.hikari.data-source-properties[sslrootcert]=/etc/ssl/certs/ca-certificates.crt\n",
	), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(project.Path, "infra"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(project.Path, "infra", "main.parameters.json"), []byte(`{
"parameters":{
"databaseAdministratorObjectId":{"value":"${DATABASE_ADMIN_OBJECT_ID}"},
"databaseAdministratorName":{"value":"${DATABASE_ADMIN_NAME}"}
}}`), 0o600))
	return project, service
}

// SpringJarFixture writes a synthetic Spring Boot JAR with the supplied archive entries.
func SpringJarFixture(t *testing.T, entries [][2]string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "app.jar")
	// #nosec G304 -- fixed filename inside t.TempDir().
	file, err := os.Create(name)
	require.NoError(t, err)
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		resource, createErr := writer.Create(entry[0])
		require.NoError(t, createErr)
		_, writeErr := resource.Write([]byte(entry[1]))
		require.NoError(t, writeErr)
	}
	require.NoError(t, writer.Close())
	require.NoError(t, file.Close())
	return name
}

// MavenPackageContext stages raw JAR bytes inside a Maven package ServiceContext fixture.
func MavenPackageContext(t *testing.T, content []byte) *azdext.ServiceContext {
	t.Helper()
	packageDir := t.TempDir()
	// #nosec G304 G703 -- fixed staged JAR name under a fresh test-owned temporary directory.
	require.NoError(t, os.WriteFile(filepath.Join(packageDir, "app.jar"), content, 0o600))
	return &azdext.ServiceContext{Package: []*azdext.Artifact{{
		Kind:         azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY,
		Location:     packageDir,
		LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
		Metadata:     map[string]string{"framework": "maven"},
	}}}
}

// PackagingProjectServer serves an in-memory ProjectConfig and on-disk azure.yaml sections over gRPC.
type PackagingProjectServer struct {
	azdext.UnimplementedProjectServiceServer
	Project *azdext.ProjectConfig
}

// Get returns the configured ProjectConfig.
func (s *PackagingProjectServer) Get(context.Context, *azdext.EmptyRequest) (*azdext.GetProjectResponse, error) {
	return &azdext.GetProjectResponse{Project: s.Project}, nil
}

// GetConfigSection loads the requested section from azure.yaml in the project directory.
func (s *PackagingProjectServer) GetConfigSection(
	ctx context.Context, request *azdext.GetProjectConfigSectionRequest,
) (*azdext.GetProjectConfigSectionResponse, error) {
	config, err := coreproject.LoadConfig(ctx, filepath.Join(s.Project.Path, "azure.yaml"))
	if err != nil {
		return nil, err
	}
	section, found := config.GetMap(request.Path)
	if !found {
		return &azdext.GetProjectConfigSectionResponse{Found: false}, nil
	}
	value, err := structpb.NewStruct(section)
	return &azdext.GetProjectConfigSectionResponse{Found: true, Section: value}, err
}

// PackagingTestClient starts an in-process gRPC AzdClient backed by PackagingProjectServer.
func PackagingTestClient(
	t *testing.T, project *azdext.ProjectConfig, containers ...azdext.ContainerServiceServer,
) *azdext.AzdClient {
	t.Helper()
	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	azdext.RegisterProjectServiceServer(server, &PackagingProjectServer{Project: project})
	for _, container := range containers {
		azdext.RegisterContainerServiceServer(server, container)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}

// LifecycleAccountServer serves mocked GetCurrentPrincipal and LookupTenant responses.
type LifecycleAccountServer struct {
	azdext.UnimplementedAccountServiceServer
	PrincipalType  azdext.PrincipalType
	ObjectID       string
	TenantID       string
	TenantError    error
	PrincipalCalls int
	TenantCalls    int
}

// GetCurrentPrincipal returns the configured developer principal.
func (s *LifecycleAccountServer) GetCurrentPrincipal(
	context.Context, *azdext.GetCurrentPrincipalRequest,
) (*azdext.GetCurrentPrincipalResponse, error) {
	s.PrincipalCalls++
	return &azdext.GetCurrentPrincipalResponse{ObjectId: s.ObjectID, PrincipalType: s.PrincipalType}, nil
}

// LookupTenant returns the configured user-access tenant ID.
func (s *LifecycleAccountServer) LookupTenant(
	context.Context, *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	s.TenantCalls++
	return &azdext.LookupTenantResponse{TenantId: s.TenantID}, s.TenantError
}

// LifecycleTestClient starts an in-process gRPC AzdClient with custom service registrations.
func LifecycleTestClient(t *testing.T, register func(*grpc.Server)) *azdext.AzdClient {
	t.Helper()
	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}
