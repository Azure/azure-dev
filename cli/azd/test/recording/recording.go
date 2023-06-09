package recording

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/exp/slog"
	"gopkg.in/dnaeon/go-vcr.v3/cassette"
	"gopkg.in/yaml.v3"
)

type RecordMode int64

const (
	Unknown RecordMode = iota
	Playback
	Record
	Live
)

type recordOption struct {
	mode RecordMode
}

type Options interface {
	Apply(r *recordOption)
}

func WithRecordMode(mode RecordMode) Options {
	return modeOption{mode: mode}
}

type modeOption struct {
	mode RecordMode
}

func (m modeOption) Apply(r *recordOption) {
	r.mode = m.mode
}

const EnvNameKey = "env_name"
const TimeKey = "time"

type Session struct {
	// ProxyUrl is the URL of the proxy server that will be recording or replaying interactions.
	ProxyUrl string

	// If true, playing back from recording.
	// Otherwise, recording.
	Playback bool

	// Variables stored in the session.
	// These variables are automatically set as environment variables for the CLI process under test.
	// See [test/azdcli] for more details.
	Variables map[string]string
}

func (s *Session) Environ() []string {
	var env []string
	for k, v := range s.Variables {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	return env
}

func (s *Session) ProxyClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		return url.Parse(s.ProxyUrl)
	}
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	client := &http.Client{Transport: transport}

	return client
}

// Start starts the recorder proxy, returning a [recording.Session] if recording or playback is enabled.
// In live mode, it returns nil.
//
// By default, the recorder proxy will log errors and info messages.
// The environment variable RECORDER_PROXY_DEBUG can be set to enable debug logging for the recorder proxy.
func Start(t *testing.T, opts ...Options) *Session {
	opt := recordOption{}
	for _, o := range opts {
		o.Apply(&opt)
	}

	dir := callingDir(1)
	name := filepath.Join(dir, "testdata", t.Name())
	baseName := filepath.Base(name)

	if opt.mode == Unknown {
		_, err := load(name)
		if errors.Is(err, os.ErrNotExist) {
			if os.Getenv("CI") != "" {
				t.Fatalf(
					"no recording available for %s. record this locally before re-running the pipeline",
					baseName)
			}

			t.Logf("playback not available for %s. recording locally", baseName)
			opt.mode = Record
		} else if err != nil {
			t.Fatalf("failed to load cassette: %v", err)
		} else {
			opt.mode = Playback
		}
	}

	writer := &logWriter{t: t}
	level := slog.LevelInfo
	if os.Getenv("RECORDER_PROXY_DEBUG") != "" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{
		Level: level,
	}))

	session := &Session{}

	var cst *cassette.Cassette
	var isPlayback bool
	var modeStr string

	switch opt.mode {
	case Live:
		return nil
	case Playback:
		r, err := load(name)
		if err != nil {
			t.Fatalf("failed to load recording: %v", err)
		}

		cst = r.Cst
		isPlayback = true
		modeStr = "playback"
		session.Variables = r.Variables
	case Record:
		cst = cassette.New(name)
		isPlayback = false
		modeStr = "record"
		session.Variables = map[string]string{}
	}

	if opt.mode != Playback && opt.mode != Record {
		t.Fatalf("invalid record mode: %v", opt.mode)
	}

	proxy := &connectHandler{
		Log: log,
		HttpHandler: &recorderProxy{
			Log: log,
			Panic: func(msg string) {
				t.Fatal(msg)
			},
			Playback: isPlayback,
			Cst:      cst,
			Passthrough: func(req *http.Request) bool {
				return strings.Contains(req.URL.Host, "login.microsoftonline.com") ||
					strings.Contains(req.URL.Host, "graph.microsoft.com")
			},
		},
	}

	server := httptest.NewTLSServer(proxy)
	proxy.TLS = server.TLS
	t.Logf("recorderProxy started with mode %s at %s", modeStr, server.URL)
	session.ProxyUrl = server.URL

	t.Cleanup(func() {
		server.Close()
		if !t.Failed() {
			err := save(recording{Cst: cst, Variables: session.Variables})
			if err != nil {
				t.Fatalf("failed to save recording: %v", err)
			}
		}
	})

	return session
}

// recording contains the items stored locally on disk for a recording.
type recording struct {
	Cst *cassette.Cassette

	Variables map[string]string
}

func load(name string) (recording, error) {
	cst, err := cassette.Load(name)
	if err != nil {
		return recording{}, fmt.Errorf("failed to load cassette: %w", err)
	}

	f, err := os.Open(cst.File)
	if err != nil {
		return recording{}, fmt.Errorf("failed to load cassette file: %w", err)
	}

	// This implementation uses a buf reader to scan for the second document delimiter for performance.
	// A more robust implementation would use the YAML decoder to scan for the second document.
	r := bufio.NewReader(f)
	docIndex := 0
	for {
		text, err := r.ReadString('\n')
		if text == "---\n" {
			docIndex++
		}

		if docIndex == 2 {
			break
		}

		// EOF
		if err != nil {
			break
		}
	}

	if docIndex != 2 {
		return recording{Cst: cst}, nil
	}

	bytes, err := ioutil.ReadAll(r)
	if err != nil {
		return recording{}, fmt.Errorf("failed to read recording file: %w", err)
	}

	var variables map[string]string
	err = yaml.Unmarshal(bytes, &variables)
	if err != nil {
		return recording{}, fmt.Errorf("failed to parse recording file: %w", err)
	}

	return recording{Cst: cst, Variables: variables}, nil
}

func save(r recording) error {
	if err := r.Cst.Save(); err != nil {
		return fmt.Errorf("failed to save interactions: %v", err)
	}

	file := r.Cst.File
	f, err := os.OpenFile(file, os.O_APPEND|os.O_RDWR, 0755)
	if err != nil {
		return err
	}

	defer f.Close()
	bytes, err := yaml.Marshal(r.Variables)
	if err != nil {
		return err
	}

	// YAML document separator, see http://www.yaml.org/spec/1.2/spec.html#id2760395
	_, err = f.WriteString("---\n")
	if err != nil {
		return err
	}

	_, err = f.Write(bytes)
	if err != nil {
		return fmt.Errorf("failed to write variables: %v", err)
	}

	err = f.Close()
	if err != nil {
		return fmt.Errorf("failed to close file: %v", err)
	}

	return nil
}

func callingDir(skip int) string {
	_, b, _, _ := runtime.Caller(skip + 1)
	return filepath.Dir(b)
}

type logWriter struct {
	t  *testing.T
	sb strings.Builder
}

func (l *logWriter) Write(bytes []byte) (n int, err error) {
	for i, b := range bytes {
		err = l.sb.WriteByte(b)
		if err != nil {
			return i, err
		}

		if b == '\n' {
			l.t.Logf(l.sb.String())
			l.sb.Reset()
		}
	}
	return len(bytes), nil
}
