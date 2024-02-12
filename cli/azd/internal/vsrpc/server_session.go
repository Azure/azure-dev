package vsrpc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"go.lsp.dev/jsonrpc2"
)

// serverSession represents a logical connection from a single client. Since our RPCs are split over multiple HTTP endpoints,
// we need a way to correlate these multiple connections together. This is the purpose of the session. Sessions are assigned
// a unique ID when they are created and are stored in the sessions map. The serverSession object holds a container (created
// via NewScope from the root container the server uses) as well as a few other bits of state. Since our RPCs run
// asynchronously, multiple RPCs can be running concurrently on the same session. This means that the serverSession object
// needs to be safe to access from multiple goroutines. sessionMu is used to protect access to the session itself.
//
// TODO(azure/azure-dev#3287): Today we lock the session at the start of each RPC and hold it for the entire lifetime of the
// RPC. In practice this can be problematic because DeployAsync and RefreshEnvironmentAsync can take a long time to run.
// Ideally we would do finer grained locking the session itself and know that the rest of the system is safe to run
// concurrently. To do this we need to get better disciplined about what state we store and how we access it, so for now
// we just do very coarse grained locking.
type serverSession struct {
	id string
	// rootPath is the path to the root of the solution.
	rootPath string
	// root container is the root container for the server. This is not expected to be modified.
	rootContainer *ioc.NestedContainer
}

// readManifest reads the manifest for the given app host. It caches the result for future calls.
func (s *serverSession) readManifest(
	ctx context.Context, appHostPath string, dotnetCli dotnet.DotNetCli,
) (*apphost.Manifest, error) {

	manifest, err := apphost.ManifestFromAppHost(ctx, appHostPath, dotnetCli)
	if err != nil {
		return nil, fmt.Errorf("failed to load app host manifest: %w", err)
	}

	return manifest, nil
}

// newSession creates a new session and returns the session ID and session. newSession is safe to call by multiple
// goroutines. A Session can be recovered from an id with [sessionFromId].
func (s *Server) newSession() (string, *serverSession, error) {
	b := make([]byte, 8)
	_, err := rand.Read(b)
	if err != nil {
		return "", nil, err
	}

	id := base64.StdEncoding.EncodeToString(b)
	session := &serverSession{}

	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	s.sessions[id] = session

	return id, session, nil
}

// sessionFromId fetches the session with the given ID, if it exists. sessionFromId is safe to call by multiple goroutines.
func (s *Server) sessionFromId(id string) (*serverSession, bool) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()

	session, ok := s.sessions[id]
	return session, ok
}

// validateSession ensures the session id is valid and returns the corresponding and serverSession object. If there
// is an error it will be of type *jsonrpc2.Error.
func (s *Server) validateSession(ctx context.Context, session Session) (*serverSession, error) {
	if session.Id == "" {
		return nil, jsonrpc2.NewError(jsonrpc2.InvalidParams, "session.Id is required")
	}

	serverSession, has := s.sessionFromId(session.Id)
	if !has {
		return nil, jsonrpc2.NewError(jsonrpc2.InvalidParams, "session.Id is invalid")
	}

	serverSession.id = session.Id
	return serverSession, nil
}
