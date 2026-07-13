package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/swaroop/aero/aerocore/pkg/api"
)

const (
	defaultTimeout            = 2 * time.Second
	clientFallbackBackendID   = "client-local-fallback"
	clientFallbackReason      = "aerocore_unavailable_local_fallback"
	emptyPlacementTargetURL   = "aerocore placement target url is empty"
	rejectedPlacementDecision = "aerocore placement rejected request"
)

type Client struct {
	baseURL     string
	httpClient  *http.Client
	fallbackURL string
}

type Option func(*Client)

type RequestError struct {
	Err error
}

func (e *RequestError) Error() string {
	return fmt.Sprintf("aerocore request failed: %v", e.Err)
}

func (e *RequestError) Unwrap() error {
	return e.Err
}

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

func WithFallbackURL(url string) Option {
	return func(c *Client) {
		c.fallbackURL = strings.TrimRight(strings.TrimSpace(url), "/")
	}
}

func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

func (c *Client) Resolve(ctx context.Context, req api.PlacementRequest) (api.PlacementResponse, error) {
	var resp api.PlacementResponse

	if c.baseURL == "" {
		return resp, fmt.Errorf("aerocore base url is empty")
	}

	headers := map[string]string{}
	if requestID := strings.TrimSpace(req.RequestID); requestID != "" {
		headers[api.IncomingRequestIDHeader] = requestID
	}

	status, err := c.doJSON(ctx, http.MethodPost, "/resolve", req, &resp, headers)
	if err != nil {
		return resp, err
	}

	if status != http.StatusOK {
		return resp, fmt.Errorf("aerocore resolve returned status %d", status)
	}

	return resp, nil
}

func (c *Client) ResolveTarget(ctx context.Context, req api.PlacementRequest) (string, api.PlacementResponse, error) {
	resp, err := c.Resolve(ctx, req)
	if err != nil {
		var requestErr *RequestError
		if errors.As(err, &requestErr) && c.fallbackURL != "" {
			fallbackResp := api.PlacementResponse{
				RequestID:  req.RequestID,
				Decision:   api.DecisionFailOpen,
				BackendID:  clientFallbackBackendID,
				BackendURL: c.fallbackURL,
				Rung:       api.RungUpstream,
				Reason:     clientFallbackReason,
				FailOpen:   true,
			}

			return c.fallbackURL, fallbackResp, nil
		}

		return "", resp, err
	}

	switch resp.Decision {
	case api.DecisionRoute, api.DecisionFailOpen:
		targetURL := strings.TrimSpace(resp.BackendURL)
		if targetURL == "" {
			return "", resp, fmt.Errorf("%s decision=%s backend_id=%s", emptyPlacementTargetURL, resp.Decision, resp.BackendID)
		}

		return targetURL, resp, nil

	case api.DecisionReject:
		return "", resp, fmt.Errorf("%s reason=%s", rejectedPlacementDecision, resp.Reason)

	default:
		return "", resp, fmt.Errorf("unknown aerocore placement decision %q", resp.Decision)
	}
}

func (c *Client) Ready(ctx context.Context) (ReadyResponse, error) {
	var resp ReadyResponse

	if c.baseURL == "" {
		return resp, fmt.Errorf("aerocore base url is empty")
	}

	status, err := c.doJSON(ctx, http.MethodGet, "/readyz", nil, &resp, nil)
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

	status, err := c.doJSON(ctx, http.MethodGet, "/config", nil, &resp, nil)
	if err != nil {
		return resp, err
	}

	if status != http.StatusOK {
		return resp, fmt.Errorf("aerocore config returned status %d", status)
	}

	return resp, nil
}

func (c *Client) doJSON(ctx context.Context, method string, path string, in any, out any, headers map[string]string) (int, error) {
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

	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			httpReq.Header.Set(key, value)
		}
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return 0, &RequestError{Err: err}
	}
	defer httpResp.Body.Close()

	if out != nil {
		if err := json.NewDecoder(httpResp.Body).Decode(out); err != nil {
			return httpResp.StatusCode, fmt.Errorf("decode response: %w", err)
		}
	}

	return httpResp.StatusCode, nil
}
