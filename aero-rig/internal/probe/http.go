package probe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const HTTPResultSchemaV1 = "aerorig.http_probe.v1"

type HTTPRequest struct {
	RunID      string
	Sample     int
	TargetName string
	TargetURL  string
	Method     string
}

type HTTPResult struct {
	SchemaVersion string  `json:"schema_version"`
	Probe         string  `json:"probe"`
	RunID         string  `json:"run_id"`
	Sample        int     `json:"sample"`
	TargetName    string  `json:"target_name"`
	TargetURL     string  `json:"target_url"`
	Method        string  `json:"method"`
	StartedAt     string  `json:"started_at"`
	DurationMS    float64 `json:"duration_ms"`
	StatusCode    int     `json:"status_code,omitempty"`
	BytesRead     int64   `json:"bytes_read"`
	ContentType   string  `json:"content_type,omitempty"`
	Server        string  `json:"server,omitempty"`
	OK            bool    `json:"ok"`
	Error         string  `json:"error,omitempty"`
}

func HTTP(ctx context.Context, client *http.Client, req HTTPRequest) HTTPResult {
	method := strings.TrimSpace(req.Method)
	if method == "" {
		method = http.MethodGet
	}

	res := HTTPResult{
		SchemaVersion: HTTPResultSchemaV1,
		Probe:         "http",
		RunID:         req.RunID,
		Sample:        req.Sample,
		TargetName:    req.TargetName,
		TargetURL:     req.TargetURL,
		Method:        method,
		StartedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}

	parsed, err := url.Parse(req.TargetURL)
	if err != nil {
		res.Error = fmt.Sprintf("invalid target url: %v", err)
		return res
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		res.Error = "target url scheme must be http or https"
		return res
	}
	if parsed.Host == "" {
		res.Error = "target url must include host"
		return res
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, req.TargetURL, nil)
	if err != nil {
		res.Error = fmt.Sprintf("build request: %v", err)
		return res
	}
	httpReq.Header.Set("User-Agent", "aerorig/0.1")

	start := time.Now()
	httpRes, err := client.Do(httpReq)
	res.DurationMS = float64(time.Since(start).Microseconds()) / 1000.0
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer httpRes.Body.Close()

	res.StatusCode = httpRes.StatusCode
	res.ContentType = httpRes.Header.Get("Content-Type")
	res.Server = httpRes.Header.Get("Server")

	n, readErr := io.Copy(io.Discard, io.LimitReader(httpRes.Body, 16<<20))
	res.BytesRead = n
	if readErr != nil {
		res.Error = fmt.Sprintf("read response: %v", readErr)
		return res
	}

	res.OK = httpRes.StatusCode >= 200 && httpRes.StatusCode < 400
	if !res.OK {
		res.Error = fmt.Sprintf("non-success status: %d", httpRes.StatusCode)
	}

	return res
}
