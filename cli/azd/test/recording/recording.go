// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// recording implements a proxy server that records and plays back HTTP interactions.
// The implementation largely reuses [go-vcr], but adds support for:
//   - Saving and loading of variables in the recording session
//   - Enabling recording on a HTTP/1.1 proxy server that uses HTTP Connect, unlike [go-vcr/recorder] which by default
//     supports client-side recording
package recording

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/test/cmdrecord"
	"gopkg.in/dnaeon/go-vcr.v3/cassette"
	"gopkg.in/dnaeon/go-vcr.v3/recorder"
	"gopkg.in/yaml.v3"
)

type recordOptions struct {
	mode recorder.Mode
}

type Options interface {
	Apply(r recordOptions) recordOptions
}

func WithRecordMode(mode recorder.Mode) Options {
	return modeOption{mode: mode}
}

type modeOption struct {
	mode recorder.Mode
}

func (in modeOption) Apply(out recordOptions) recordOptions {
	out.mode = in.mode
	return out
}

const EnvNameKey = "env_name"
const TimeKey = "time"
const SubscriptionIdKey = "subscription_id"

type Session struct {
	// ProxyUrl is the URL of the proxy server that will be recording or replaying interactions.
	ProxyUrl string

	// CmdProxyPaths are the paths that should be appended to PATH to proxy any cmd invocations.
	CmdProxyPaths []string

	// If true, playing back from recording.
	Playback bool

	// Variables stored in the session.
	Variables map[string]string

	// A http.Client that is configured to communicate through the proxy server.
	ProxyClient *http.Client
}

// Start starts the recorder proxy, returning a [recording.Session].
// In live mode, it returns nil. By default, interactions are automatically recorded once
// if no recording is available on disk.
// To set the record mode, specify AZURE_RECORD_MODE='live', 'playback', or 'record'. To control the exact behavior
// in a test, pass WithRecordMode to Start.
//
// Start automatically adds the required t.Cleanup to save recordings when the test succeeds,
// and handles shutting down the proxy server.
//
// By default, the recorder proxy will log error and info messages.
// The environment variable RECORDER_PROXY_DEBUG can be set to enable debug logging for the recorder proxy.
func Start(t *testing.T, opts ...Options) *Session {
	opt := recordOptions{}
	// for local dev, use recordOnce which will record once if no recording isn't available on disk.
	// if the recording is available, it will playback.
	if os.Getenv("CI") == "" {
		opt.mode = recorder.ModeRecordOnce
	}

	// Set defaults based on AZURE_RECORD_MODE
	if os.Getenv("AZURE_RECORD_MODE") != "" {
		switch strings.ToLower(os.Getenv("AZURE_RECORD_MODE")) {
		case "live":
			opt.mode = recorder.ModePassthrough
		case "playback":
			opt.mode = recorder.ModeReplayOnly
		case "record":
			opt.mode = recorder.ModeRecordOnly
		default:
			t.Fatalf(
				"unsupported AZURE_RECORD_MODE: %s , valid options are: record, live, playback",
				os.Getenv("AZURE_RECORD_MODE"))
		}
	}

	// Apply user-defined options
	for _, o := range opts {
		opt = o.Apply(opt)
	}

	// Return nil for live mode
	if opt.mode == recorder.ModePassthrough {
		return nil
	}

	dir := callingDir(1)
	name := filepath.Join(dir, "testdata", "recordings", t.Name())
	writer := &logWriter{t: t}
	level := slog.LevelInfo
	if os.Getenv("RECORDER_PROXY_DEBUG") != "" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level: level,
	}))

	recorderOptions := &recorder.Options{
		CassetteName:       name,
		Mode:               opt.mode,
		SkipRequestLatency: true,
	}

	// This also automatically loads the recording.
	vcr, err := recorder.NewWithOptions(recorderOptions)
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed to load recordings: %v: %s",
			err,
			"to record this test, re-run the test with AZURE_RECORD_MODE='record'")
	} else if err != nil {
		t.Fatalf("failed to load recordings: %v", err)
	}

	session := &Session{}
	if opt.mode == recorder.ModeReplayOnly {
		session.Playback = true
	} else if opt.mode == recorder.ModeRecordOnce && !vcr.IsNewCassette() {
		session.Playback = true
	}

	if session.Playback {
		variables, err := loadVariables(name + ".yaml")
		if err != nil {
			t.Fatalf("failed to load variables: %v", err)
		}
		session.Variables = variables
		if session.Variables == nil { // prefer empty map over nil
			session.Variables = map[string]string{}
		}
	} else {
		session.Variables = map[string]string{}
		session.Variables[TimeKey] = fmt.Sprintf("%d", time.Now().Unix())
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	vcr.SetRealTransport(&gzip2HttpRoundTripper{
		transport: transport,
	})

	vcr.SetMatcher(func(r *http.Request, i cassette.Request) bool {
		// Ignore query parameter 's=...' in containerappOperationResults
		if strings.Contains(r.URL.Path, "/providers/Microsoft.App/") &&
			strings.Contains(r.URL.Path, "/containerappOperationResults") {
			recorded, err := url.Parse(i.URL)
			if err != nil {
				panic(err)
			}

			recorded.RawQuery = ""
			r.URL.RawQuery = ""
			log.Info("recorderProxy: ignoring query parameters in containerappOperationResults", "url", r.URL)
			return r.Method == i.Method && r.URL.String() == recorded.String()
		}

		return cassette.DefaultMatcher(r, i)
	})

	// Fast-forward polling operations
	discarder := httpPollDiscarder{}
	vcr.AddHook(discarder.BeforeSave, recorder.BeforeSaveHook)

	// Trim GET subscriptions-level deployment responses
	vcr.AddHook(func(i *cassette.Interaction) error {
		return TrimSubscriptionsDeployment(i, session.Variables)
	}, recorder.BeforeSaveHook)

	// Sanitize
	vcr.AddHook(func(i *cassette.Interaction) error {
		i.Request.Headers.Set("Authorization", "SANITIZED")

		err := sanitizeContainerAppTokenExchange(i)
		if err != nil {
			log.Error("failed to sanitize container app token exchange", "error", err)
		}

		err = sanitizeContainerAppListSecrets(i)
		if err != nil {
			log.Error("failed to sanitize container app list secrets", "error", err)
		}

		err = sanitizeContainerAppUpdate(i)
		if err != nil {
			log.Error("failed to sanitize container app update", "error", err)
		}

		return nil
	}, recorder.BeforeSaveHook)

	vcr.AddHook(func(i *cassette.Interaction) error {
		if i.DiscardOnSave {
			log.Debug("recorderProxy: discarded response", "url", i.Request.URL, "status", i.Response.Code)
		}
		return nil
	}, recorder.BeforeSaveHook)

	vcr.AddHook(func(i *cassette.Interaction) error {
		if vcr.IsRecording() {
			log.Debug("recorderProxy: recording response", "url", i.Request.URL, "status", i.Response.Code)
		} else {
			log.Debug("recorderProxy: replaying response", "url", i.Request.URL, "status", i.Response.Code)
		}
		return nil
	}, recorder.BeforeResponseReplayHook)

	// Add passthrough for services that return personal data and need not be recorded
	vcr.AddPassthrough(func(req *http.Request) bool {
		return strings.Contains(req.URL.Host, "login.microsoftonline.com") ||
			strings.Contains(req.URL.Host, "graph.microsoft.com") ||
			strings.Contains(req.URL.Host, "applicationinsights.azure.com") ||
			(strings.Contains(req.URL.Host, "aka.ms") &&
				strings.Contains(req.URL.Path, "/azure-dev")) ||
			strings.Contains(req.URL.Host, "azure-dev.azureedge.net") ||
			strings.Contains(req.URL.Host, "azdrelease.azureedge.net") ||
			strings.Contains(req.URL.Host, "default.exp-tas.com")
	})

	proxy := &connectHandler{
		Log: log,
		HttpHandler: &recorderProxy{
			Log: log,
			Panic: func(msg string) {
				t.Fatal("recorderProxy: " + msg)
			},
			Recorder: vcr,
		},
	}

	var recorders []*cmdrecord.Recorder
	recorders = append(recorders, cmdrecord.NewWithOptions(cmdrecord.Options{
		CmdName:      "docker",
		CassetteName: name,
		RecordMode:   opt.mode,
		Intercepts: []cmdrecord.Intercept{
			{ArgsMatch: "^login"},
			{ArgsMatch: "^push"},
		},
	}))
	recorders = append(recorders, cmdrecord.NewWithOptions(cmdrecord.Options{
		CmdName:      "dotnet",
		CassetteName: name,
		RecordMode:   opt.mode,
		Intercepts: []cmdrecord.Intercept{
			{ArgsMatch: "^publish(.*?)-p:ContainerRegistry="},
		},
	}))

	for _, r := range recorders {
		path, err := r.Start()
		if err != nil {
			t.Fatalf("failed to start cmd recorder: %v", err)
		}
		session.CmdProxyPaths = append(session.CmdProxyPaths, path)
	}

	server := httptest.NewTLSServer(proxy)
	proxy.TLS = server.TLS
	t.Logf("recorderProxy started with mode %v at %s", displayMode(vcr), server.URL)
	session.ProxyUrl = server.URL

	client, err := proxyClient(server.URL)
	if err != nil {
		t.Fatalf("failed to create proxy client: %v", err)
	}
	session.ProxyClient = client

	t.Cleanup(func() {
		server.Close()
		if !t.Failed() {
			shouldSave := vcr.IsRecording()
			err = vcr.Stop()
			if err != nil {
				t.Fatalf("failed to save recording: %v", err)
			}

			if shouldSave {
				err = saveVariables(recorderOptions.CassetteName+".yaml", session.Variables)
				if err != nil {
					t.Fatalf("failed to save variables: %v", err)
				}
			}

			for _, r := range recorders {
				err = r.Stop()
				if err != nil {
					t.Fatalf("failed to save cmd recording: %v", err)
				}
			}

		}
	})

	return session
}

func proxyClient(proxyUrl string) (*http.Client, error) {
	proxyAddr, err := url.Parse(proxyUrl)
	if err != nil {
		return nil, err
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		return proxyAddr, nil
	}
	//nolint:gosec
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	client := &http.Client{Transport: transport}
	return client, nil
}

var modeStrMap = map[recorder.Mode]string{
	recorder.ModeRecordOnly: "record",
	recorder.ModeRecordOnce: "recordOnce",

	recorder.ModeReplayOnly:  "replay",
	recorder.ModePassthrough: "live",
}

func displayMode(vcr *recorder.Recorder) string {
	mode := vcr.Mode()
	if mode == recorder.ModeRecordOnce {
		actualMode := "playback"
		if vcr.IsNewCassette() {
			actualMode = "record"
		}
		return fmt.Sprintf("%s (%s)", modeStrMap[mode], actualMode)
	}

	return modeStrMap[mode]
}

// Loads variables from disk.
// When loading from disk, the variables are expected to be the second document in the provided yaml file.
func loadVariables(name string) (map[string]string, error) {
	f, err := os.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load cassette file: %w", err)
	}

	r := bufio.NewReader(f)
	docIndex := 0
	for {
		text, err := r.ReadString('\n')
		if text == "---\n" || text == "---\r\n" {
			docIndex++
		}

		if docIndex == 2 { // found the second document containing variables
			break
		}

		// EOF
		if err != nil {
			break
		}
	}

	if docIndex != 2 { // no variables
		return nil, nil
	}

	bytes, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read recording file: %w", err)
	}

	var variables map[string]string
	err = yaml.Unmarshal(bytes, &variables)
	if err != nil {
		return nil, fmt.Errorf("failed to parse recording file: %w", err)
	}
	return variables, nil
}

// Saves variables into the named file. The variables are appended as a separate YAML document to the file.
func saveVariables(name string, variables map[string]string) error {
	f, err := os.OpenFile(name, os.O_APPEND|os.O_RDWR, 0755)
	if err != nil {
		return err
	}

	defer f.Close()
	bytes, err := yaml.Marshal(variables)
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
		return fmt.Errorf("failed to write variables: %w", err)
	}

	err = f.Close()
	if err != nil {
		return fmt.Errorf("failed to close file: %w", err)
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
