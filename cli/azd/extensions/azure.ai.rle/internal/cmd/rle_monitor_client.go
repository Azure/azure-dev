// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"azure.ai.rle/internal/monitor"
	"azure.ai.rle/internal/rollouts"
)

// Fine-tuning job IDs carry 24 or 32 lowercase hexadecimal characters.
var rleJobIDPattern = regexp.MustCompile(`^ftjob-(?:[0-9a-f]{24}|[0-9a-f]{32})$`)
var monitorErrorCodePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,80}$`)

type rleJobSource struct {
	rle   *rleClient
	jobID string
}

func (s *rleJobSource) read(ctx context.Context, path string, target any, limit int64) error {
	return readMonitorJSON(ctx, s.rle.httpClient, s.rle.baseUrl, path,
		s.rle.authorizationHeader, nil, target, limit)
}

func (s *rleJobSource) jobPath() string {
	return environmentCollectionPath + "/jobs/" + url.PathEscape(s.jobID)
}

func (s *rleJobSource) Config(ctx context.Context) (json.RawMessage, error) {
	job, err := s.readJob(ctx)
	return job.Metadata, err
}

type rleMonitorJob struct {
	ID       string          `json:"id"`
	Status   string          `json:"status"`
	Metadata json.RawMessage `json:"metadata"`
}

func (s *rleJobSource) readJob(ctx context.Context) (rleMonitorJob, error) {
	var job rleMonitorJob
	if !rleJobIDPattern.MatchString(s.jobID) {
		return job, errors.New("invalid RLE job ID")
	}
	var raw json.RawMessage
	if err := s.read(ctx, s.jobPath(), &raw, 1<<20); err != nil {
		return job, err
	}
	if err := json.Unmarshal(raw, &job); err != nil {
		return rleMonitorJob{}, fmt.Errorf("decode RLE job response: %w", err)
	}
	if job.ID != s.jobID || strings.TrimSpace(job.Status) == "" {
		return rleMonitorJob{}, errors.New("invalid RLE job status response")
	}
	metadata := bytes.TrimSpace(job.Metadata)
	if len(metadata) == 0 || metadata[0] != '{' {
		return rleMonitorJob{}, errors.New("RLE job metadata must be an object")
	}
	return job, nil
}

func monitorPageQuery(key string, after int64, token string) string {
	query := url.Values{key: {strconv.FormatInt(after, 10)}, "limit": {"100"}}
	if token != "" {
		query.Set("continuationToken", token)
	}
	return "?" + query.Encode()
}

func (s *rleJobSource) Metrics(
	ctx context.Context, after int64, token string,
) (monitor.JobPage[json.RawMessage], error) {
	var page monitor.JobPage[json.RawMessage]
	err := s.read(ctx, s.jobPath()+"/metrics"+monitorPageQuery("lastStep", after, token), &page, 16<<20)
	if err == nil && page.Data == nil {
		err = errors.New("metrics response is missing its data array")
	}
	return page, err
}

// Rollouts reads one page of the job's rollouts, oldest first. It never sends
// lastSequence, so rollouts without a sampler sequence_id are included; the
// service rejects after and createdAfter, so a scan advances only by token.
func (s *rleJobSource) Rollouts(
	ctx context.Context, token string,
) (monitor.JobPage[monitor.JobRollout], error) {
	query := url.Values{"limit": {"100"}}
	if token != "" {
		query.Set("continuationToken", token)
	}
	var page monitor.JobPage[monitor.JobRollout]
	err := s.read(ctx, s.jobPath()+"/rollouts?"+query.Encode(), &page, 4<<20)
	if err != nil {
		return page, err
	}
	if page.Data == nil {
		return page, errors.New("rollouts response is missing its data array")
	}
	for _, entry := range page.Data {
		if rollouts.ValidateID(entry.RolloutID) != nil || entry.JobID != s.jobID {
			return page, errors.New("invalid rollout summary for the monitored job")
		}
	}
	return page, nil
}

func (s *rleJobSource) Detail(ctx context.Context, id string) (monitor.JobRollout, error) {
	var entry monitor.JobRollout
	err := s.read(ctx, environmentCollectionPath+"/rollouts/"+url.PathEscape(id), &entry, 1<<20)
	if err == nil && entry.RolloutID != id {
		err = errors.New("rollout metadata does not match the requested rollout")
	}
	return entry, err
}

func (s *rleJobSource) Result(ctx context.Context, entry monitor.JobRollout) (rollouts.Snapshot, error) {
	var snapshot rollouts.Snapshot
	err := s.read(ctx, environmentCollectionPath+"/rollouts/"+url.PathEscape(entry.RolloutID)+"/result",
		&snapshot.Response, 32<<20)
	if err != nil {
		return snapshot, err
	}
	snapshot.Source = "RLE service"
	snapshot.Environment = &rollouts.Environment{Name: entry.EnvironmentName, Version: entry.EnvironmentVer}
	if entry.SavedAt != nil {
		snapshot.SavedAt = *entry.SavedAt
	}
	return snapshot, nil
}

func (s *rleJobSource) Events(
	ctx context.Context, id, token string,
) (monitor.JobPage[json.RawMessage], error) {
	var page monitor.JobPage[json.RawMessage]
	if err := rollouts.ValidateID(id); err != nil {
		return page, err
	}
	query := url.Values{"limit": {"100"}}
	if token != "" {
		query.Set("continuationToken", token)
	}
	err := s.read(ctx, environmentCollectionPath+"/rollouts/"+url.PathEscape(id)+"/events?"+query.Encode(),
		&page, 32<<20)
	if err == nil && page.Data == nil {
		err = errors.New("rollout events response is missing its data array")
	}
	return page, err
}

func (s *rleJobSource) Status(ctx context.Context) (string, error) {
	if !rleJobIDPattern.MatchString(s.jobID) {
		return "", errors.New("invalid RLE job ID")
	}
	var job finetuneJobResource
	// The project proxy supplies fine-tuning routing and uses Foundry authentication.
	if err := s.read(ctx, finetuneJobsPath+"/"+url.PathEscape(s.jobID), &job, 1<<20); err != nil {
		return "", err
	}
	if job.Id != s.jobID || strings.TrimSpace(job.Status) == "" {
		return "", errors.New("invalid fine-tuning job status response")
	}
	return job.Status, nil
}

func readMonitorJSON(
	ctx context.Context, client *http.Client, base, path string,
	authorize func(context.Context) (string, error), headers map[string]string, target any, limit int64,
) error {
	u, err := url.Parse(base + path)
	if err != nil {
		return errors.New("invalid monitor endpoint")
	}
	if u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return errors.New("monitor requires an HTTPS endpoint without embedded credentials")
	}
	if strings.HasPrefix(path, environmentCollectionPath) {
		query := u.Query()
		query.Set("api-version", foundryAPIVersion)
		u.RawQuery = query.Encode()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return errors.New("could not construct monitor request")
	}
	token, err := authorize(ctx)
	if err != nil {
		return fmt.Errorf("authenticate monitor: %w", err)
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Accept", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	// Never follow service-supplied locations with the operator's credentials.
	httpClient := *client
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := httpClient.Do(req)
	if err != nil {
		if requestErr, ok := errors.AsType[*url.Error](err); ok {
			return fmt.Errorf("monitor request failed: %w", requestErr.Err)
		}
		return fmt.Errorf("monitor request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		readErr := &monitor.ReadError{Status: resp.StatusCode, Code: http.StatusText(resp.StatusCode)}
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds >= 0 {
			readErr.RetryAfter = time.Duration(min(seconds, 3600)) * time.Second
		} else if deadline, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
			readErr.RetryAfter = max(0, time.Until(deadline))
		}
		readErr.Code, readErr.Operation, readErr.Request = monitorErrorDetails(resp, readErr.Code)
		return readErr
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fmt.Errorf("read monitor response: %w", err)
	}
	if int64(len(data)) > limit {
		return errors.New("monitor response exceeds its size limit")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode monitor response: %w", err)
	}
	return nil
}

// monitorErrorDetails keeps only the machine-readable error code and the
// correlation IDs needed for investigation, never an opaque service body.
// Codes appear at the top level or under "error"; RLE reports its operation
// ID under "correlation".
func monitorErrorDetails(resp *http.Response, fallback string) (code, operation, request string) {
	code = fallback
	var body struct {
		Code        string          `json:"code"`
		Error       json.RawMessage `json:"error"`
		Correlation struct {
			Operation string `json:"operation"`
			Request   string `json:"request"`
		} `json:"correlation"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body) == nil {
		var nested struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(body.Error, &nested) == nil && monitorErrorCodePattern.MatchString(nested.Code) {
			code = nested.Code
		} else if monitorErrorCodePattern.MatchString(body.Code) {
			code = body.Code
		}
		if correlationIDPattern.MatchString(body.Correlation.Operation) {
			operation = body.Correlation.Operation
		}
		request = body.Correlation.Request
	}
	if !correlationIDPattern.MatchString(request) {
		request = ""
	}
	if headerRequest := serviceRequestID(resp.Header, nil); headerRequest != "" {
		request = headerRequest
	}
	return code, operation, request
}
