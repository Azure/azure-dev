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
	opt := recordOptions{}
	// for local dev, use recordOnce which will record once if no recording isn't available on disk.
	// if the recording is available, it will playback.
	if os.Getenv("CI") == "" {
		opt.mode = recorder.ModeRecordOnce
	}

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

	for _, o := range opts {
		opt = o.Apply(opt)
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

	session := &Session{
		Variables: map[string]string{},
	}

	recorderOptions := &recorder.Options{
		CassetteName:       name,
		Mode:               opt.mode,
		SkipRequestLatency: false,
	}

	// This also automatically loads the recording.
	vcr, err := recorder.NewWithOptions(recorderOptions)
	if err != nil {
		t.Fatalf("failed to load recordings: %v", err)
	}
	err = loadVariables(name+".yaml", &session.Variables)
	if err != nil {
		t.Fatalf("failed to load variables: %v", err)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	vcr.SetRealTransport(&gzip2HttpRoundTripper{
		transport: transport,
	})

	vcr.AddHook(func(i *cassette.Interaction) error {
		i.Request.Headers.Del("Authorization")
		return nil
	}, recorder.BeforeSaveHook)

	discarder := httpPollDiscarder{}
	vcr.AddHook(discarder.BeforeSave, recorder.BeforeSaveHook)

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

	vcr.AddPassthrough(func(req *http.Request) bool {
		return strings.Contains(req.URL.Host, "login.microsoftonline.com") ||
			strings.Contains(req.URL.Host, "graph.microsoft.com")
	})

	proxy := &connectHandler{
		Log: log,
		HttpHandler: &recorderProxy{
			Log: log,
			Panic: func(msg string) {
				t.Fatal(msg)
			},
			Recorder: vcr,
		},
	}

	server := httptest.NewTLSServer(proxy)
	proxy.TLS = server.TLS
	t.Logf("recorderProxy started with mode %v at %s", displayMode(vcr), server.URL)
	session.ProxyUrl = server.URL

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
		}
	})

	return session
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

// Loads variables from disk. The variables are expected to be the second document in the provided yaml file.
// If the file doesn't exist, returns nil.
func loadVariables(name string, variables *map[string]string) error {
	f, err := os.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to load cassette file: %w", err)
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

		if docIndex == 2 { // found the second document containing variables
			break
		}

		// EOF
		if err != nil {
			break
		}
	}

	if docIndex != 2 { // no variables
		return nil
	}

	bytes, err := ioutil.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read recording file: %w", err)
	}

	err = yaml.Unmarshal(bytes, &variables)
	if err != nil {
		return fmt.Errorf("failed to parse recording file: %w", err)
	}

	return nil
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
