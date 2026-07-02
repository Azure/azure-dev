// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/require"
)

type fakeCredential struct{}

func (fakeCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// newTestClient uses the insecure-http constructor so the bearer policy
// doesn't reject the plain-HTTP httptest endpoint.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return newClient(srv.URL, fakeCredential{}, "test", true)
}

func TestClient_CreateVersionInline_SendsRequestEnvelope(t *testing.T) {
	var capturedAPI string
	var capturedContentType string
	var capturedPath string
	var capturedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAPI = r.URL.Query().Get("api-version")
		capturedContentType = r.Header.Get("Content-Type")
		capturedPath = r.URL.Path
		require.Equal(t, http.MethodPost, r.Method)

		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &capturedBody))

		w.Header().Set("Content-Type", ContentTypeJSON)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"ver_1","skill_id":"sk_1","name":"my-skill","version":"1","description":"d","created_at":1}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.CreateVersionInline(context.Background(), "my-skill", CreateVersionRequest{
		InlineContent: &SkillInlineContent{Description: "d", Instructions: "body"},
		Default:       true,
	})
	require.NoError(t, err)
	require.Equal(t, "/skills/my-skill/versions", capturedPath)
	require.Equal(t, DataPlaneAPIVersion, capturedAPI)
	require.Equal(t, ContentTypeJSON, capturedContentType)

	inline, ok := capturedBody["inline_content"].(map[string]any)
	require.True(t, ok, "inline_content must be present")
	require.Equal(t, "d", inline["description"])
	require.Equal(t, "body", inline["instructions"])
	require.Equal(t, true, capturedBody["default"])

	require.Equal(t, "ver_1", got.ID)
	require.Equal(t, "1", got.Version)
	require.Equal(t, "sk_1", got.SkillID)
}

func TestClient_CreateVersionFromZip_SendsMultipart(t *testing.T) {
	var capturedPath string
	var capturedCT string
	var capturedFiles []string
	var capturedDefault string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedCT = r.Header.Get("Content-Type")

		_, params, err := mime.ParseMediaType(capturedCT)
		require.NoError(t, err)
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			data, _ := io.ReadAll(part)
			switch part.FormName() {
			case "files":
				capturedFiles = append(capturedFiles, part.FileName())
				require.Equal(t, ContentTypeZip, part.Header.Get("Content-Type"))
				require.Equal(t, "PK\x03\x04fake", string(data))
			case "default":
				capturedDefault = string(data)
			}
		}

		w.Header().Set("Content-Type", ContentTypeJSON)
		_, _ = io.WriteString(w, `{"id":"ver_1","skill_id":"sk_1","name":"my-skill","version":"1","description":"d","created_at":1}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.CreateVersionFromZip(
		context.Background(), "my-skill", "my-skill.zip",
		strings.NewReader("PK\x03\x04fake"), true,
	)
	require.NoError(t, err)
	require.Equal(t, "/skills/my-skill/versions", capturedPath)
	require.True(t, strings.HasPrefix(capturedCT, "multipart/form-data"), capturedCT)
	require.Equal(t, []string{"my-skill.zip"}, capturedFiles)
	require.Equal(t, "true", capturedDefault)
	require.Equal(t, "1", got.Version)
}

func TestClient_GetSkill_DecodesEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/skills/my-skill", r.URL.Path)
		_, _ = io.WriteString(w, `{"id":"sk_1","name":"my-skill","description":"d","default_version":"2","latest_version":"3","created_at":42}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.GetSkill(context.Background(), "my-skill")
	require.NoError(t, err)
	require.Equal(t, "sk_1", got.ID)
	require.Equal(t, "2", got.DefaultVersion)
	require.Equal(t, "3", got.LatestVersion)
	require.Equal(t, int64(42), got.CreatedAt)
}

func TestClient_UpdateSkillDefaultVersion_SendsBody(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/skills/my-skill", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &captured))
		_, _ = io.WriteString(w, `{"id":"sk_1","name":"my-skill","description":"d","default_version":"2","latest_version":"3","created_at":1}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.UpdateSkillDefaultVersion(context.Background(), "my-skill", "2")
	require.NoError(t, err)
	require.Equal(t, "2", captured["default_version"])
	require.Equal(t, "2", got.DefaultVersion)
}

func TestClient_ListSkills_FlattensPagination(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/skills", r.URL.Path)
		switch page {
		case 0:
			require.Empty(t, r.URL.Query().Get("after"))
			_, _ = io.WriteString(w, `{"data":[{"name":"a"},{"name":"b"}],"has_more":true,"last_id":"b"}`)
		case 1:
			require.Equal(t, "b", r.URL.Query().Get("after"))
			_, _ = io.WriteString(w, `{"data":[{"name":"c"}],"has_more":false}`)
		default:
			t.Fatalf("unexpected extra page request: %d", page)
		}
		page++
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	all, err := c.ListAllSkills(context.Background(), ListOptions{}, 0)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c"}, []string{all[0].Name, all[1].Name, all[2].Name})
}

func TestClient_ListSkills_HonorsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "5", r.URL.Query().Get("limit"))
		require.Equal(t, "desc", r.URL.Query().Get("order"))
		_, _ = io.WriteString(w, `{"data":[{"name":"a"},{"name":"b"},{"name":"c"}],"has_more":true,"last_id":"c"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	all, err := c.ListAllSkills(context.Background(), ListOptions{Limit: 5, Order: "desc"}, 2)
	require.NoError(t, err)
	require.Len(t, all, 2)
}

func TestClient_DownloadSkillContent_ValidatesContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/skills/my-skill/content", r.URL.Path)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "not zip")
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.DownloadSkillContent(context.Background(), "my-skill")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected download content type")
}

func TestClient_DownloadSkillContent_ReturnsZipBytes(t *testing.T) {
	payload := []byte("PK\x03\x04fake-zip-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/skills/my-skill/content", r.URL.Path)
		w.Header().Set("Content-Type", ContentTypeZip)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.DownloadSkillContent(context.Background(), "my-skill")
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

func TestClient_DownloadSkillContent_RejectsOversizeContentLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := strings.Repeat("A", 17)
		w.Header().Set("Content-Type", ContentTypeZip)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	c := newTestClient(t, srv).WithMaxDownloadBytes(16)
	_, err := c.DownloadSkillContent(context.Background(), "my-skill")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds")
}

func TestClient_DownloadSkillContent_RejectsOversizeStreamingBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ContentTypeZip)
		// No Content-Length: force the streaming-limit path.
		// Stream chunks until the client closes the connection.
		flusher, _ := w.(http.Flusher)
		chunk := []byte("AAAAAAAA") // 8 bytes
		for range 16 {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv).WithMaxDownloadBytes(16)
	_, err := c.DownloadSkillContent(context.Background(), "my-skill")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds")
}

func TestClient_DownloadSkillContent_AcceptsAtLimit(t *testing.T) {
	payload := []byte("PK\x03\x04tinybody")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ContentTypeZip)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	c := newTestClient(t, srv).WithMaxDownloadBytes(16)
	got, err := c.DownloadSkillContent(context.Background(), "my-skill")
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

func TestClient_DownloadVersionContent_TargetsVersionPath(t *testing.T) {
	payload := []byte("PK\x03\x04v2")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/skills/my-skill/versions/2/content", r.URL.Path)
		w.Header().Set("Content-Type", ContentTypeZip)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.DownloadVersionContent(context.Background(), "my-skill", "2")
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

func TestClient_DeleteSkill_ReturnsServiceResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/skills/my-skill", r.URL.Path)
		_, _ = io.WriteString(w, `{"id":"sk_1","name":"my-skill","deleted":true}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	resp, err := c.DeleteSkill(context.Background(), "my-skill")
	require.NoError(t, err)
	require.True(t, resp.Deleted)
	require.Equal(t, "my-skill", resp.Name)
	require.Equal(t, "sk_1", resp.ID)
}

func TestClient_DeleteSkillVersion_ReturnsServiceResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/skills/my-skill/versions/2", r.URL.Path)
		_, _ = io.WriteString(w, `{"id":"ver_1","name":"my-skill","version":"2","deleted":true}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	resp, err := c.DeleteSkillVersion(context.Background(), "my-skill", "2")
	require.NoError(t, err)
	require.True(t, resp.Deleted)
	require.Equal(t, "2", resp.Version)
}

func TestClient_ListSkillVersions_Decodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/skills/my-skill/versions", r.URL.Path)
		_, _ = io.WriteString(w, `{"data":[{"name":"my-skill","version":"1"},{"name":"my-skill","version":"2"}],"has_more":false}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	page, err := c.ListSkillVersions(context.Background(), "my-skill", ListOptions{}, "")
	require.NoError(t, err)
	require.Len(t, page.Data, 2)
	require.Equal(t, "1", page.Data[0].Version)
	require.Equal(t, "2", page.Data[1].Version)
}

func TestClient_GetSkillVersion_Decodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/skills/my-skill/versions/2", r.URL.Path)
		_, _ = io.WriteString(w, `{"id":"ver_2","skill_id":"sk_1","name":"my-skill","version":"2","description":"d","created_at":1}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	got, err := c.GetSkillVersion(context.Background(), "my-skill", "2")
	require.NoError(t, err)
	require.Equal(t, "2", got.Version)
	require.Equal(t, "ver_2", got.ID)
}
