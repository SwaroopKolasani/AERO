package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/swaroop/aero/aerocore/internal/placement"
)

const defaultTimeout = 2 * time.Second

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type Option func(*Client)

func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		if timeout > 0 {
			c.httpClient.Timeout = timeout
		}
	}
}

func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

func (c *Client) Resolve(ctx context.Context, req placement.PlacementRequest) (placement.PlacementResponse, error) {
	var resp placement.PlacementResponse

	if c.baseURL == "" {
		return resp, fmt.Errorf("aerocore base url is empty")
	}

	status, err := c.doJSON(ctx, http.MethodPost, "/resolve", req, &resp)
	if err != nil {
		return resp, err
	}

	if status != http.StatusOK {
		return resp, fmt.Errorf("aerocore resolve returned status %d", status)
	}

	return resp, nil
}

func (c *Client) Ready(ctx context.Context) (ReadyResponse, error) {
	var resp ReadyResponse

	if c.baseURL == "" {
		return resp, fmt.Errorf("aerocore base url is empty")
	}

	status, err := c.doJSON(ctx, http.MethodGet, "/readyz", nil, &resp)
	if err != nil {
		return resp, err
	}

	if status != http.StatusOK && status != http.StatusServiceUnavailable {
		return resp, fmt.Errorf("aerocore readyz returned status %d", status)
	}

	return resp, nil
}

func (c *Client) Config(ctx context.Context) (ConfigResponse, error) {
	var resp ConfigResponse

	if c.baseURL == "" {
		return resp, fmt.Errorf("aerocore base url is empty")
	}

	status, err := c.doJSON(ctx, http.MethodGet, "/config", nil, &resp)
	if err != nil {
		return resp, err
	}

	if status != http.StatusOK {
		return resp, fmt.Errorf("aerocore config returned status %d", status)
	}

	return resp, nil
}

func (c *Client) doJSON(ctx context.Context, method string, path string, in any, out any) (int, error) {
	var body *bytes.Reader

	if in == nil {
		body = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(in)
		if err != nil {
			return 0, fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(data)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}

	if in != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("aerocore request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if out != nil {
		if err := json.NewDecoder(httpResp.Body).Decode(out); err != nil {
			return httpResp.StatusCode, fmt.Errorf("decode response: %w", err)
		}
	}

	return httpResp.StatusCode, nil
}
