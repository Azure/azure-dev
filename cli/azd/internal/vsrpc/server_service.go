// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/mattn/go-colorable"
)

// serverService is the RPC server for the '/ServerService/v1.0' endpoint.
type serverService struct {
	server *Server
}

func newServerService(server *Server) *serverService {
	return &serverService{
		server: server,
	}
}

// InitializeAsync is the server implementation of:
// ValueTask<Session> InitializeAsync(string rootPath, CancellationToken cancellationToken);
func (s *serverService) InitializeAsync(ctx context.Context, rootPath string) (*Session, error) {
	// TODO(azure/azure-dev#3288): Ideally the Chdir would be be something we injected into components instead of it being
	// ambient authority. We'll get there, but for now let's also just Chdir into the root folder so places where we use
	// a relative path will work.
	//
	// In practice we do not expect multiple clients with different root paths to be calling into the same server. If you
	// need that today, launch a new server for each root path...
	if err := os.Chdir(rootPath); err != nil {
		return nil, err
	}

	id, session, err := s.server.newSession()
	if err != nil {
		return nil, err
	}

	session.rootPath = rootPath
	session.rootContainer = s.server.rootContainer

	return &Session{
		Id: id,
	}, nil
}

type container struct {
	*ioc.NestedContainer
	outWriter *writerMultiplexer
	errWriter *writerMultiplexer
}

func newContainer(s *serverSession) *container {
	c, err := s.rootContainer.NewScope()
	if err != nil {
		panic(err)
	}
	id := s.id
	azdCtx := azdcontext.NewAzdContextWithDirectory(s.rootPath)

	outWriter := newWriter(fmt.Sprintf("[%s stdout] ", id))
	errWriter := newWriter(fmt.Sprintf("[%s stderr] ", id))

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

	c.MustRegisterScoped(func() input.Console {
		stdout := outWriter
		stderr := errWriter
		stdin := strings.NewReader("")
		writer := colorable.NewNonColorable(stdout)

		return input.NewConsole(true, false, writer, input.ConsoleHandles{
			Stdin:  stdin,
			Stdout: stdout,
			Stderr: stderr,
		}, &output.NoneFormatter{})
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

	return &container{
		NestedContainer: c,
		outWriter:       outWriter,
		errWriter:       errWriter,
	}
}

// StopAsync is the server implementation of:
// ValueTask StopAsync(CancellationToken cancellationToken);
func (s *serverService) StopAsync(ctx context.Context) error {
	// TODO(azure/azure-dev#3286): Need to think about how shutdown works. For now it is probably best to just have the
	// client terminate `azd` once they know all outstanding RPCs have completed instead of trying to do a graceful
	// shutdown on our end.
	return nil
}

// ServeHTTP implements http.Handler.
func (s *serverService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serveRpc(w, r, map[string]Handler{
		"InitializeAsync": HandlerFunc1(s.InitializeAsync),
		"StopAsync":       HandlerAction0(s.StopAsync),
	})
}

// newWriter returns a *writerMultiplexer that has a default writer that writes to log.Printf with the given prefix.
func newWriter(prefix string) *writerMultiplexer {
	wm := &writerMultiplexer{}
	wm.AddWriter(writerFunc(func(p []byte) (n int, err error) {
		log.Printf("%s%s", prefix, string(p))
		return n, nil
	}))

	return wm
}
