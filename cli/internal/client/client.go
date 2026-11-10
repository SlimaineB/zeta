package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	Cookie  string
	HTTP    *http.Client
}

type Profile struct {
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Language        string   `json:"language"`
	Image           string   `json:"image"`
	DefaultRepo     string   `json:"defaultRepo"`
	ArtifactExclude []string `json:"artifactExclude,omitempty"`
}

type Build struct {
	ID              string  `json:"id"`
	Profile         string  `json:"profile"`
	Language        string  `json:"language"`
	Repo            string  `json:"repo"`
	Commit          string  `json:"commit"`
	Owner           string  `json:"owner"`
	Status          string  `json:"status"`
	CreatedAt       string  `json:"createdAt"`
	FinishedAt      *string `json:"finishedAt,omitempty"`
	DurationMs      int64   `json:"durationMs"`
	InitDurationMs  int64   `json:"initDurationMs"`
	BuildDurationMs int64   `json:"buildDurationMs"`
}

type Volume struct {
	Name          string     `json:"name"`
	Role          string     `json:"role"`
	CapacityBytes int64      `json:"capacityBytes"`
	StorageClass  string     `json:"storageClass"`
	CreatedAt     time.Time  `json:"createdAt"`
	LastUsedAt    *time.Time `json:"lastUsedAt,omitempty"`
	LastBuild     string     `json:"lastBuild,omitempty"`
	LastProfile   string     `json:"lastProfile,omitempty"`
	LastOwner     string     `json:"lastOwner,omitempty"`
	Resizable     bool       `json:"resizable"`
	InUse         bool       `json:"inUse"`
}

type VolumeQuota struct {
	MaxTotalBytes  int64 `json:"maxTotalBytes"`
	UsedBytes      int64 `json:"usedBytes"`
	RemainingBytes int64 `json:"remainingBytes"`
	MaxVolumeBytes int64 `json:"maxVolumeBytes"`
}

type VolumeList struct {
	Volumes []Volume    `json:"volumes"`
	Quota   VolumeQuota `json:"quota"`
}

type SubmitRequest struct {
	Profile string `json:"profile"`
	Repo    string `json:"repo"`
	Commit  string `json:"commit"`
	Sync    bool   `json:"sync,omitempty"`
}

func New(server, cookie string) (*Client, error) {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if server == "" {
		return nil, fmt.Errorf("serveur requis (--server ou ZETA_SERVER)")
	}
	u, err := url.Parse(server)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("URL de serveur invalide: %s", server)
	}
	return &Client{
		BaseURL: server,
		Cookie:  cookie,
		HTTP:    &http.Client{Timeout: 0},
	}, nil
}

func (c *Client) Login(ctx context.Context, email, password string) (string, error) {
	res, err := c.request(ctx, http.MethodPost, "/api/login", map[string]string{
		"username": email,
		"password": password,
	})
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	for _, ck := range res.Cookies() {
		if ck.Name == "zeta_session" {
			return ck.Value, nil
		}
	}
	return "", fmt.Errorf("session cookie missing from response")
}

func (c *Client) Profiles(ctx context.Context) ([]Profile, error) {
	var out []Profile
	if err := c.getJSON(ctx, "/api/profiles", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) Submit(ctx context.Context, req SubmitRequest) (string, error) {
	res, err := c.request(ctx, http.MethodPost, "/api/builds", req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	return decodeBuildID(res.Body)
}

func (c *Client) SubmitContext(ctx context.Context, req SubmitRequest, contextPath string) (string, error) {
	f, err := os.Open(contextPath)
	if err != nil {
		return "", err
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		defer f.Close()
		var writeErr error
		for _, field := range [][2]string{{"profile", req.Profile}, {"repo", req.Repo}, {"commit", req.Commit}, {"sync", fmt.Sprintf("%t", req.Sync)}} {
			if writeErr = mw.WriteField(field[0], field[1]); writeErr != nil {
				break
			}
		}
		if writeErr == nil {
			part, err := mw.CreateFormFile("context", "context.tar.gz")
			if err != nil {
				writeErr = err
			} else {
				_, writeErr = io.Copy(part, f)
			}
		}
		if err := mw.Close(); writeErr == nil {
			writeErr = err
		}
		_ = pw.CloseWithError(writeErr)
	}()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/builds", pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		return "", err
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())
	if header := cookieHeader(c.Cookie); header != "" {
		httpReq.Header.Set("Cookie", header)
	}
	res, err := c.HTTP.Do(httpReq)
	if err != nil {
		_ = pr.CloseWithError(err)
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return "", apiError(res)
	}
	return decodeBuildID(res.Body)
}

func (c *Client) SyncURL(id string) (string, error) {
	return c.wsURL("/api/sync/" + url.PathEscape(id) + "/client")
}

func (c *Client) wsURL(path string) (string, error) {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = ""
	return u.String(), nil
}

func decodeBuildID(r io.Reader) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("identifiant de build absent")
	}
	return out.ID, nil
}

func (c *Client) Builds(ctx context.Context) ([]Build, error) {
	var out []Build
	if err := c.getJSON(ctx, "/api/builds", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) StreamLogs(ctx context.Context, id string, w io.Writer) error {
	res, err := c.request(ctx, http.MethodGet, "/api/builds/"+url.PathEscape(id)+"/logs", nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, err = io.Copy(w, res.Body)
	return err
}

func (c *Client) Metrics(ctx context.Context, id string) (json.RawMessage, error) {
	res, err := c.request(ctx, http.MethodGet, "/api/builds/"+url.PathEscape(id)+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return io.ReadAll(res.Body)
}

func (c *Client) Volumes(ctx context.Context) (VolumeList, error) {
	var out VolumeList
	if err := c.getJSON(ctx, "/api/volumes", &out); err != nil {
		return VolumeList{}, err
	}
	return out, nil
}

func (c *Client) ResizeVolume(ctx context.Context, name, size string) error {
	res, err := c.request(ctx, http.MethodPost, "/api/volumes/"+url.PathEscape(name)+"/resize", map[string]string{"size": size})
	if err != nil {
		return err
	}
	return res.Body.Close()
}

func (c *Client) DeleteVolume(ctx context.Context, name string) error {
	res, err := c.request(ctx, http.MethodDelete, "/api/volumes/"+url.PathEscape(name), nil)
	if err != nil {
		return err
	}
	return res.Body.Close()
}

type Environment struct {
	ID           string    `json:"id"`
	Profile      string    `json:"profile"`
	Active       bool      `json:"active"`
	SizeBytes    int64     `json:"sizeBytes"`
	StorageClass string    `json:"storageClass"`
	CreatedAt    time.Time `json:"createdAt"`
}

type EnvRequest struct {
	Name    string            `json:"name"`
	Profile string            `json:"profile"`
	Image   string            `json:"image,omitempty"`
	Size    string            `json:"size,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func (c *Client) CreateEnv(ctx context.Context, req EnvRequest) (Environment, error) {
	var out Environment
	res, err := c.request(ctx, http.MethodPost, "/api/envs", req)
	if err != nil {
		return Environment{}, err
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return Environment{}, err
	}
	return out, nil
}

func (c *Client) Environments(ctx context.Context) ([]Environment, error) {
	var out []Environment
	if err := c.getJSON(ctx, "/api/envs", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) ActivateEnv(ctx context.Context, id string) error {
	res, err := c.request(ctx, http.MethodPost, "/api/envs/"+url.PathEscape(id)+"/activate", map[string]any{})
	if err != nil {
		return err
	}
	return res.Body.Close()
}

func (c *Client) DeactivateEnv(ctx context.Context, id string) error {
	res, err := c.request(ctx, http.MethodPost, "/api/envs/"+url.PathEscape(id)+"/deactivate", map[string]any{})
	if err != nil {
		return err
	}
	return res.Body.Close()
}

func (c *Client) DeleteEnv(ctx context.Context, id string) error {
	res, err := c.request(ctx, http.MethodDelete, "/api/envs/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	return res.Body.Close()
}

func (c *Client) EnvExecURL(id string) (string, error) {
	return c.wsURL("/api/envs/" + url.PathEscape(id) + "/exec")
}

func (c *Client) StreamArtifact(ctx context.Context, id string, w io.Writer) error {
	res, err := c.request(ctx, http.MethodGet, "/api/builds/"+url.PathEscape(id)+"/artifact", nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, err = io.Copy(w, res.Body)
	return err
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	res, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return json.NewDecoder(res.Body).Decode(out)
}

func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if header := cookieHeader(c.Cookie); header != "" {
		req.Header.Set("Cookie", header)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		defer res.Body.Close()
		return nil, apiError(res)
	}
	return res, nil
}

func cookieHeader(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "=") {
		return raw
	}
	return "zeta_session=" + raw
}

func apiError(res *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &payload) == nil && payload.Error != "" {
		if res.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("%s (session expired? run zeta login)", payload.Error)
		}
		return fmt.Errorf("%s", payload.Error)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		text = res.Status
	}
	return fmt.Errorf("%s", text)
}
