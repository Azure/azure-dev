package vsrpc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/mattn/go-colorable"
	"go.lsp.dev/jsonrpc2"
)

// serverSession represents a logical connection from a single client with a given azd project path.
// Since our RPCs are split over multiple HTTP endpoints, we need a way to correlate these multiple connections together.
// This is the purpose of the session.
// Sessions are assigned a unique ID when they are created and are stored in the sessions map.
type serverSession struct {
	id string
	// rootPath is the path to the root of the solution.
	rootPath string
	// root container points to server.rootContainer
	rootContainer            *ioc.NestedContainer
	externalServicesEndpoint string
	externalServicesKey      string
	externalServicesClient   *http.Client
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

type container struct {
	*ioc.NestedContainer
	outWriter     *writerMultiplexer
	errWriter     *writerMultiplexer
	spinnerWriter *writerMultiplexer
}

// newContainer creates a new container for the session.
func (s *serverSession) newContainer(rc RequestContext) (*container, error) {
	c, err := s.rootContainer.NewScopeRegistrationsOnly()
	if err != nil {
		return nil, err
	}

	id := s.id
	azdCtx, err := azdContext(rc.HostProjectPath)
	if err != nil {
		return nil, err
	}

	outWriter := newWriter(fmt.Sprintf("[%s stdout] ", id))
	errWriter := newWriter(fmt.Sprintf("[%s stderr] ", id))
	spinnerWriter := newWriter(fmt.Sprintf("[%s spinner] ", id))
	// Useful for debugging, direct all the output to the console, so you can see it in VS Code.
	outWriter.AddWriter(&lineWriter{
		next: writerFunc(func(p []byte) (n int, err error) {
			os.Stdout.Write([]byte(fmt.Sprintf("[%s stdout] %s", id, string(p))))
			return n, nil
		}),
	})

	errWriter.AddWriter(&lineWriter{
		next: writerFunc(func(p []byte) (n int, err error) {
			os.Stdout.Write([]byte(fmt.Sprintf("[%s stderr] %s", id, string(p))))
			return n, nil
		}),
	})

	spinnerWriter.AddWriter(&lineWriter{
		next: writerFunc(func(p []byte) (n int, err error) {
			os.Stdout.Write([]byte(fmt.Sprintf("[%s spinner] %s", id, string(p))))
			return n, nil
		}),
	})

	c.MustRegisterScoped(func() input.Console {
		stdout := outWriter
		stderr := errWriter
		stdin := strings.NewReader("")
		writer := colorable.NewNonColorable(stdout)

		return input.NewConsole(true, false, input.Writers{
			Output:  writer,
			Spinner: colorable.NewNonColorable(spinnerWriter)},
			input.ConsoleHandles{
				Stdin:  stdin,
				Stdout: stdout,
				Stderr: stderr,
			},
			&output.NoneFormatter{},
			&input.ExternalPromptConfiguration{
				Endpoint: s.externalServicesEndpoint,
				Key:      s.externalServicesKey,
				Client:   s.externalServicesClient,
			})
	})

	c.MustRegisterScoped(func(console input.Console) io.Writer {
		return colorable.NewNonColorable(console.Handles().Stdout)
	})

	c.MustRegisterScoped(func() *internal.GlobalCommandOptions {
		return &internal.GlobalCommandOptions{
			NoPrompt: true,
		}
	})

	c.MustRegisterScoped(func() *azdcontext.AzdContext {
		return azdCtx
	})

	c.MustRegisterScoped(func() *lazy.Lazy[*azdcontext.AzdContext] {
		return lazy.From(azdCtx)
	})

	c.MustRegisterScoped(func() auth.ExternalAuthConfiguration {
		return auth.ExternalAuthConfiguration{
			Endpoint: s.externalServicesEndpoint,
			Key:      s.externalServicesKey,
			Client:   s.externalServicesClient,
		}
	})

	return &container{
		NestedContainer: c,
		outWriter:       outWriter,
		errWriter:       errWriter,
		spinnerWriter:   spinnerWriter,
	}, nil
}
