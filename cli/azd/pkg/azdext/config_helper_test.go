// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"google.golang.org/grpc"
)

// --- Stub UserConfigService ---

type stubUserConfigService struct {
	getResp       *GetUserConfigResponse
	getStringResp *GetUserConfigStringResponse
	getSectionErr error
	getErr        error
	getStringErr  error
	setErr        error
	unsetErr      error
}

func (s *stubUserConfigService) Get(
	_ context.Context, _ *GetUserConfigRequest, _ ...grpc.CallOption,
) (*GetUserConfigResponse, error) {
	return s.getResp, s.getErr
}

func (s *stubUserConfigService) GetString(
	_ context.Context, _ *GetUserConfigStringRequest, _ ...grpc.CallOption,
) (*GetUserConfigStringResponse, error) {
	return s.getStringResp, s.getStringErr
}

func (s *stubUserConfigService) GetSection(
	_ context.Context, _ *GetUserConfigSectionRequest, _ ...grpc.CallOption,
) (*GetUserConfigSectionResponse, error) {
	return nil, s.getSectionErr
}

func (s *stubUserConfigService) Set(
	_ context.Context, _ *SetUserConfigRequest, _ ...grpc.CallOption,
) (*EmptyResponse, error) {
	return &EmptyResponse{}, s.setErr
}

func (s *stubUserConfigService) Unset(
	_ context.Context, _ *UnsetUserConfigRequest, _ ...grpc.CallOption,
) (*EmptyResponse, error) {
	return &EmptyResponse{}, s.unsetErr
}

// --- Stub EnvironmentService ---

type stubEnvironmentService struct {
	getConfigResp       *GetConfigResponse
	getConfigStringResp *GetConfigStringResponse
	getConfigErr        error
	getConfigStringErr  error
	setConfigErr        error
	unsetConfigErr      error
}

func (s *stubEnvironmentService) GetCurrent(
	_ context.Context, _ *EmptyRequest, _ ...grpc.CallOption,
) (*EnvironmentResponse, error) {
	return nil, nil
}

func (s *stubEnvironmentService) List(
	_ context.Context, _ *EmptyRequest, _ ...grpc.CallOption,
) (*EnvironmentListResponse, error) {
	return nil, nil
}

func (s *stubEnvironmentService) Get(
	_ context.Context, _ *GetEnvironmentRequest, _ ...grpc.CallOption,
) (*EnvironmentResponse, error) {
	return nil, nil
}

func (s *stubEnvironmentService) Select(
	_ context.Context, _ *SelectEnvironmentRequest, _ ...grpc.CallOption,
) (*EmptyResponse, error) {
	return nil, nil
}

func (s *stubEnvironmentService) GetValues(
	_ context.Context, _ *GetEnvironmentRequest, _ ...grpc.CallOption,
) (*KeyValueListResponse, error) {
	return nil, nil
}

func (s *stubEnvironmentService) GetValue(
	_ context.Context, _ *GetEnvRequest, _ ...grpc.CallOption,
) (*KeyValueResponse, error) {
	return nil, nil
}

func (s *stubEnvironmentService) SetValue(
	_ context.Context, _ *SetEnvRequest, _ ...grpc.CallOption,
) (*EmptyResponse, error) {
	return nil, nil
}

func (s *stubEnvironmentService) GetConfig(
	_ context.Context, _ *GetConfigRequest, _ ...grpc.CallOption,
) (*GetConfigResponse, error) {
	return s.getConfigResp, s.getConfigErr
}

func (s *stubEnvironmentService) GetConfigString(
	_ context.Context, _ *GetConfigStringRequest, _ ...grpc.CallOption,
) (*GetConfigStringResponse, error) {
	return s.getConfigStringResp, s.getConfigStringErr
}

func (s *stubEnvironmentService) GetConfigSection(
	_ context.Context, _ *GetConfigSectionRequest, _ ...grpc.CallOption,
) (*GetConfigSectionResponse, error) {
	return nil, nil
}

func (s *stubEnvironmentService) SetConfig(
	_ context.Context, _ *SetConfigRequest, _ ...grpc.CallOption,
) (*EmptyResponse, error) {
	return &EmptyResponse{}, s.setConfigErr
}

func (s *stubEnvironmentService) UnsetConfig(
	_ context.Context, _ *UnsetConfigRequest, _ ...grpc.CallOption,
) (*EmptyResponse, error) {
	return &EmptyResponse{}, s.unsetConfigErr
}

// --- NewConfigHelper ---

func TestNewConfigHelper_NilClient(t *testing.T) {
	t.Parallel()

	_, err := NewConfigHelper(nil)
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestNewConfigHelper_Success(t *testing.T) {
	t.Parallel()

	client := &AzdClient{}
	ch, err := NewConfigHelper(client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ch == nil {
		t.Fatal("expected non-nil ConfigHelper")
	}
}

// --- GetUserString ---

func TestGetUserString_EmptyPath(t *testing.T) {
	t.Parallel()

	client := &AzdClient{}
	ch, _ := NewConfigHelper(client)

	_, _, err := ch.GetUserString(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestGetUserString_Found(t *testing.T) {
	t.Parallel()

	stub := &stubUserConfigService{
		getStringResp: &GetUserConfigStringResponse{Value: "8080", Found: true},
	}

	client := &AzdClient{userConfigClient: stub}
	ch, _ := NewConfigHelper(client)

	val, found, err := ch.GetUserString(context.Background(), "extensions.myext.port")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !found {
		t.Error("expected found = true")
	}

	if val != "8080" {
		t.Errorf("value = %q, want %q", val, "8080")
	}
}

func TestGetUserString_NotFound(t *testing.T) {
	t.Parallel()

	stub := &stubUserConfigService{
		getStringResp: &GetUserConfigStringResponse{Value: "", Found: false},
	}

	client := &AzdClient{userConfigClient: stub}
	ch, _ := NewConfigHelper(client)

	val, found, err := ch.GetUserString(context.Background(), "nonexistent.path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if found {
		t.Error("expected found = false")
	}

	if val != "" {
		t.Errorf("value = %q, want empty", val)
	}
}

func TestGetUserString_GRPCError(t *testing.T) {
	t.Parallel()

	stub := &stubUserConfigService{
		getStringErr: errors.New("grpc unavailable"),
	}

	client := &AzdClient{userConfigClient: stub}
	ch, _ := NewConfigHelper(client)

	_, _, err := ch.GetUserString(context.Background(), "some.path")
	if err == nil {
		t.Fatal("expected error for gRPC failure")
	}
}

// --- GetUserJSON ---

func TestGetUserJSON_Found(t *testing.T) {
	t.Parallel()

	type myConfig struct {
		Port int    `json:"port"`
		Host string `json:"host"`
	}

	data, _ := json.Marshal(myConfig{Port: 3000, Host: "localhost"})
	stub := &stubUserConfigService{
		getResp: &GetUserConfigResponse{Value: data, Found: true},
	}

	client := &AzdClient{userConfigClient: stub}
	ch, _ := NewConfigHelper(client)

	var cfg myConfig
	found, err := ch.GetUserJSON(context.Background(), "extensions.myext", &cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !found {
		t.Error("expected found = true")
	}

	if cfg.Port != 3000 {
		t.Errorf("Port = %d, want 3000", cfg.Port)
	}

	if cfg.Host != "localhost" {
		t.Errorf("Host = %q, want %q", cfg.Host, "localhost")
	}
}

func TestGetUserJSON_NotFound(t *testing.T) {
	t.Parallel()

	stub := &stubUserConfigService{
		getResp: &GetUserConfigResponse{Value: nil, Found: false},
	}

	client := &AzdClient{userConfigClient: stub}
	ch, _ := NewConfigHelper(client)

	var cfg map[string]any
	found, err := ch.GetUserJSON(context.Background(), "nonexistent", &cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if found {
		t.Error("expected found = false")
	}
}

func TestGetUserJSON_NilOut(t *testing.T) {
	t.Parallel()

	client := &AzdClient{userConfigClient: &stubUserConfigService{}}
	ch, _ := NewConfigHelper(client)

	_, err := ch.GetUserJSON(context.Background(), "some.path", nil)
	if err == nil {
		t.Fatal("expected error for nil out parameter")
	}
}

func TestGetUserJSON_InvalidJSON(t *testing.T) {
	t.Parallel()

	stub := &stubUserConfigService{
		getResp: &GetUserConfigResponse{Value: []byte("not json"), Found: true},
	}

	client := &AzdClient{userConfigClient: stub}
	ch, _ := NewConfigHelper(client)

	var cfg map[string]any
	_, err := ch.GetUserJSON(context.Background(), "bad.json", &cfg)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("error type = %T, want *ConfigError", err)
	}

	if cfgErr.Reason != ConfigReasonInvalidFormat {
		t.Errorf("Reason = %v, want %v", cfgErr.Reason, ConfigReasonInvalidFormat)
	}
}

func TestGetUserJSON_EmptyPath(t *testing.T) {
	t.Parallel()

	client := &AzdClient{}
	ch, _ := NewConfigHelper(client)

	var cfg map[string]any
	_, err := ch.GetUserJSON(context.Background(), "", &cfg)
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

// --- SetUserJSON ---

func TestSetUserJSON_Success(t *testing.T) {
	t.Parallel()

	stub := &stubUserConfigService{}

	client := &AzdClient{userConfigClient: stub}
	ch, _ := NewConfigHelper(client)

	err := ch.SetUserJSON(context.Background(), "extensions.myext.port", 3000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetUserJSON_EmptyPath(t *testing.T) {
	t.Parallel()

	client := &AzdClient{}
	ch, _ := NewConfigHelper(client)

	err := ch.SetUserJSON(context.Background(), "", "value")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestSetUserJSON_NilValue(t *testing.T) {
	t.Parallel()

	client := &AzdClient{userConfigClient: &stubUserConfigService{}}
	ch, _ := NewConfigHelper(client)

	err := ch.SetUserJSON(context.Background(), "some.path", nil)
	if err == nil {
		t.Fatal("expected error for nil value")
	}
}

func TestSetUserJSON_GRPCError(t *testing.T) {
	t.Parallel()

	stub := &stubUserConfigService{
		setErr: errors.New("grpc write error"),
	}

	client := &AzdClient{userConfigClient: stub}
	ch, _ := NewConfigHelper(client)

	err := ch.SetUserJSON(context.Background(), "some.path", "value")
	if err == nil {
		t.Fatal("expected error for gRPC failure")
	}
}

func TestSetUserJSON_UnmarshalableValue(t *testing.T) {
	t.Parallel()

	stub := &stubUserConfigService{}
	client := &AzdClient{userConfigClient: stub}
	ch, _ := NewConfigHelper(client)

	// Channels cannot be marshaled to JSON
	err := ch.SetUserJSON(context.Background(), "some.path", make(chan int))
	if err == nil {
		t.Fatal("expected error for unmarshalable value")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("error type = %T, want *ConfigError", err)
	}

	if cfgErr.Reason != ConfigReasonInvalidFormat {
		t.Errorf("Reason = %v, want %v", cfgErr.Reason, ConfigReasonInvalidFormat)
	}
}

// --- UnsetUser ---

func TestUnsetUser_Success(t *testing.T) {
	t.Parallel()

	stub := &stubUserConfigService{}
	client := &AzdClient{userConfigClient: stub}
	ch, _ := NewConfigHelper(client)

	err := ch.UnsetUser(context.Background(), "some.path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUnsetUser_EmptyPath(t *testing.T) {
	t.Parallel()

	client := &AzdClient{}
	ch, _ := NewConfigHelper(client)

	err := ch.UnsetUser(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

// --- GetEnvString ---

func TestGetEnvString_Found(t *testing.T) {
	t.Parallel()

	stub := &stubEnvironmentService{
		getConfigStringResp: &GetConfigStringResponse{Value: "prod", Found: true},
	}

	client := &AzdClient{environmentClient: stub}
	ch, _ := NewConfigHelper(client)

	val, found, err := ch.GetEnvString(context.Background(), "extensions.myext.mode")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !found {
		t.Error("expected found = true")
	}

	if val != "prod" {
		t.Errorf("value = %q, want %q", val, "prod")
	}
}

func TestGetEnvString_NotFound(t *testing.T) {
	t.Parallel()

	stub := &stubEnvironmentService{
		getConfigStringResp: &GetConfigStringResponse{Value: "", Found: false},
	}

	client := &AzdClient{environmentClient: stub}
	ch, _ := NewConfigHelper(client)

	_, found, err := ch.GetEnvString(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if found {
		t.Error("expected found = false")
	}
}

func TestGetEnvString_EmptyPath(t *testing.T) {
	t.Parallel()

	client := &AzdClient{}
	ch, _ := NewConfigHelper(client)

	_, _, err := ch.GetEnvString(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

// --- GetEnvJSON ---

func TestGetEnvJSON_Found(t *testing.T) {
	t.Parallel()

	type envConfig struct {
		Debug bool `json:"debug"`
	}

	data, _ := json.Marshal(envConfig{Debug: true})
	stub := &stubEnvironmentService{
		getConfigResp: &GetConfigResponse{Value: data, Found: true},
	}

	client := &AzdClient{environmentClient: stub}
	ch, _ := NewConfigHelper(client)

	var cfg envConfig
	found, err := ch.GetEnvJSON(context.Background(), "extensions.myext", &cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !found {
		t.Error("expected found = true")
	}

	if !cfg.Debug {
		t.Error("expected Debug = true")
	}
}

func TestGetEnvJSON_NotFound(t *testing.T) {
	t.Parallel()

	stub := &stubEnvironmentService{
		getConfigResp: &GetConfigResponse{Value: nil, Found: false},
	}

	client := &AzdClient{environmentClient: stub}
	ch, _ := NewConfigHelper(client)

	var cfg map[string]any
	found, err := ch.GetEnvJSON(context.Background(), "nonexistent", &cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if found {
		t.Error("expected found = false")
	}
}

func TestGetEnvJSON_NilOut(t *testing.T) {
	t.Parallel()

	client := &AzdClient{environmentClient: &stubEnvironmentService{}}
	ch, _ := NewConfigHelper(client)

	_, err := ch.GetEnvJSON(context.Background(), "some.path", nil)
	if err == nil {
		t.Fatal("expected error for nil out parameter")
	}
}

// --- SetEnvJSON ---

func TestSetEnvJSON_Success(t *testing.T) {
	t.Parallel()

	stub := &stubEnvironmentService{}
	client := &AzdClient{environmentClient: stub}
	ch, _ := NewConfigHelper(client)

	err := ch.SetEnvJSON(context.Background(), "extensions.myext.mode", "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetEnvJSON_EmptyPath(t *testing.T) {
	t.Parallel()

	client := &AzdClient{}
	ch, _ := NewConfigHelper(client)

	err := ch.SetEnvJSON(context.Background(), "", "value")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestSetEnvJSON_NilValue(t *testing.T) {
	t.Parallel()

	client := &AzdClient{environmentClient: &stubEnvironmentService{}}
	ch, _ := NewConfigHelper(client)

	err := ch.SetEnvJSON(context.Background(), "some.path", nil)
	if err == nil {
		t.Fatal("expected error for nil value")
	}
}

// --- UnsetEnv ---

func TestUnsetEnv_Success(t *testing.T) {
	t.Parallel()

	stub := &stubEnvironmentService{}
	client := &AzdClient{environmentClient: stub}
	ch, _ := NewConfigHelper(client)

	err := ch.UnsetEnv(context.Background(), "some.path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUnsetEnv_EmptyPath(t *testing.T) {
	t.Parallel()

	client := &AzdClient{}
	ch, _ := NewConfigHelper(client)

	err := ch.UnsetEnv(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

// --- MergeJSON ---

func TestMergeJSON_Basic(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": 1, "b": 2}
	override := map[string]any{"b": 3, "c": 4}

	result := MergeJSON(base, override)

	if result["a"] != 1 {
		t.Errorf("a = %v, want 1", result["a"])
	}

	if result["b"] != 3 {
		t.Errorf("b = %v, want 3 (override wins)", result["b"])
	}

	if result["c"] != 4 {
		t.Errorf("c = %v, want 4", result["c"])
	}
}

func TestMergeJSON_EmptyBase(t *testing.T) {
	t.Parallel()

	result := MergeJSON(nil, map[string]any{"x": "y"})

	if result["x"] != "y" {
		t.Errorf("x = %v, want y", result["x"])
	}
}

func TestMergeJSON_EmptyOverride(t *testing.T) {
	t.Parallel()

	result := MergeJSON(map[string]any{"x": "y"}, nil)

	if result["x"] != "y" {
		t.Errorf("x = %v, want y", result["x"])
	}
}

func TestMergeJSON_BothEmpty(t *testing.T) {
	t.Parallel()

	result := MergeJSON(nil, nil)

	if len(result) != 0 {
		t.Errorf("len(result) = %d, want 0", len(result))
	}
}

func TestMergeJSON_DoesNotMutateInputs(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": 1}
	override := map[string]any{"b": 2}

	_ = MergeJSON(base, override)

	if _, ok := base["b"]; ok {
		t.Error("MergeJSON mutated base map")
	}

	if _, ok := override["a"]; ok {
		t.Error("MergeJSON mutated override map")
	}
}

// --- DeepMergeJSON ---

func TestDeepMergeJSON_RecursiveMerge(t *testing.T) {
	t.Parallel()

	base := map[string]any{
		"server": map[string]any{
			"host": "localhost",
			"port": 3000,
		},
		"debug": false,
	}

	override := map[string]any{
		"server": map[string]any{
			"port": 8080,
			"tls":  true,
		},
		"version": "1.0",
	}

	result := DeepMergeJSON(base, override)

	server, ok := result["server"].(map[string]any)
	if !ok {
		t.Fatal("server should be a map")
	}

	if server["host"] != "localhost" {
		t.Errorf("server.host = %v, want localhost", server["host"])
	}

	if server["port"] != 8080 {
		t.Errorf("server.port = %v, want 8080 (override wins)", server["port"])
	}

	if server["tls"] != true {
		t.Errorf("server.tls = %v, want true", server["tls"])
	}

	if result["debug"] != false {
		t.Errorf("debug = %v, want false", result["debug"])
	}

	if result["version"] != "1.0" {
		t.Errorf("version = %v, want 1.0", result["version"])
	}
}

func TestDeepMergeJSON_OverrideReplacesNonMap(t *testing.T) {
	t.Parallel()

	base := map[string]any{"x": "string-value"}
	override := map[string]any{"x": map[string]any{"nested": true}}

	result := DeepMergeJSON(base, override)

	nested, ok := result["x"].(map[string]any)
	if !ok {
		t.Fatal("override should replace string with map")
	}

	if nested["nested"] != true {
		t.Errorf("x.nested = %v, want true", nested["nested"])
	}
}

func TestDeepMergeJSON_DoesNotMutateInputs(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": map[string]any{"x": 1}}
	override := map[string]any{"a": map[string]any{"y": 2}}

	_ = DeepMergeJSON(base, override)

	baseA := base["a"].(map[string]any)
	if _, ok := baseA["y"]; ok {
		t.Error("DeepMergeJSON mutated base nested map")
	}
}

// --- ValidateConfig ---

func TestValidateConfig_EmptyData(t *testing.T) {
	t.Parallel()

	err := ValidateConfig("test.path", nil)
	if err == nil {
		t.Fatal("expected error for empty data")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("error type = %T, want *ConfigError", err)
	}

	if cfgErr.Reason != ConfigReasonMissing {
		t.Errorf("Reason = %v, want %v", cfgErr.Reason, ConfigReasonMissing)
	}
}

func TestValidateConfig_InvalidJSON(t *testing.T) {
	t.Parallel()

	err := ValidateConfig("test.path", []byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("error type = %T, want *ConfigError", err)
	}

	if cfgErr.Reason != ConfigReasonInvalidFormat {
		t.Errorf("Reason = %v, want %v", cfgErr.Reason, ConfigReasonInvalidFormat)
	}
}

func TestValidateConfig_ValidatorFails(t *testing.T) {
	t.Parallel()

	data, _ := json.Marshal(map[string]any{"a": 1})
	failValidator := func(_ any) error { return errors.New("validation failed") }

	err := ValidateConfig("test.path", data, failValidator)
	if err == nil {
		t.Fatal("expected error from failing validator")
	}

	var cfgErr *ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("error type = %T, want *ConfigError", err)
	}

	if cfgErr.Reason != ConfigReasonValidationFailed {
		t.Errorf("Reason = %v, want %v", cfgErr.Reason, ConfigReasonValidationFailed)
	}
}

func TestValidateConfig_AllValidatorsPass(t *testing.T) {
	t.Parallel()

	data, _ := json.Marshal(map[string]any{"a": 1, "b": 2})
	passValidator := func(_ any) error { return nil }

	err := ValidateConfig("test.path", data, passValidator, passValidator)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateConfig_NoValidators(t *testing.T) {
	t.Parallel()

	data, _ := json.Marshal(map[string]any{"a": 1})

	err := ValidateConfig("test.path", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- RequiredKeys ---

func TestRequiredKeys_AllPresent(t *testing.T) {
	t.Parallel()

	validator := RequiredKeys("host", "port")
	value := map[string]any{"host": "localhost", "port": 3000, "extra": true}

	err := validator(value)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequiredKeys_MissingKey(t *testing.T) {
	t.Parallel()

	validator := RequiredKeys("host", "port")
	value := map[string]any{"host": "localhost"}

	err := validator(value)
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestRequiredKeys_NotAMap(t *testing.T) {
	t.Parallel()

	validator := RequiredKeys("key")

	err := validator("not a map")
	if err == nil {
		t.Fatal("expected error for non-map value")
	}
}

// --- ConfigError ---

func TestConfigError_Error(t *testing.T) {
	t.Parallel()

	err := &ConfigError{
		Path:   "test.path",
		Reason: ConfigReasonMissing,
		Err:    errors.New("not found"),
	}

	got := err.Error()
	if got == "" {
		t.Fatal("Error() returned empty string")
	}
}

func TestConfigError_Unwrap(t *testing.T) {
	t.Parallel()

	inner := errors.New("inner error")
	err := &ConfigError{
		Path:   "test.path",
		Reason: ConfigReasonInvalidFormat,
		Err:    inner,
	}

	if !errors.Is(err, inner) {
		t.Error("Unwrap should expose inner error via errors.Is")
	}
}

func TestConfigReason_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reason ConfigReason
		want   string
	}{
		{ConfigReasonMissing, "missing"},
		{ConfigReasonInvalidFormat, "invalid_format"},
		{ConfigReasonValidationFailed, "validation_failed"},
		{ConfigReason(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.reason.String(); got != tt.want {
			t.Errorf("ConfigReason(%d).String() = %q, want %q", tt.reason, got, tt.want)
		}
	}
}
