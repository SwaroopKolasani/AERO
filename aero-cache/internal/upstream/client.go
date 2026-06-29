package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	http    *http.Client
}

type Config struct {
	BaseURL string
	Timeout time.Duration
}

type Result struct {
	StatusCode  int
	Header      http.Header
	Body        []byte
	ContentType string
	TokensOut   int
	OriginTier  string
}

func NewClient(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://localhost:11434"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Minute
	}

	return &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		http: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// Do posts the original OpenAI-compatible request to the direct upstream.
// If streamTo is non-nil, bytes are streamed to the client while also being
// copied into the returned buffer for write-back.
func (c *Client) Do(
	ctx context.Context,
	endpoint string,
	body []byte,
	streamTo http.ResponseWriter,
	markWritten func(),
) (*Result, error) {
	url := c.baseURL + endpoint

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}

	if streamTo != nil {
		copyReplayHeaders(streamTo.Header(), resp.Header)
		streamTo.Header().Set("Content-Type", contentType)

		if markWritten != nil {
			markWritten()
		}

		streamTo.WriteHeader(resp.StatusCode)
	}

	var buf bytes.Buffer

	tmp := make([]byte, 32*1024)
	flusher, _ := streamTo.(http.Flusher)

	for {
		n, readErr := resp.Body.Read(tmp)
		if n > 0 {
			chunk := tmp[:n]

			buf.Write(chunk)

			if streamTo != nil {
				if _, err := streamTo.Write(chunk); err != nil {
					return nil, err
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}

	out := buf.Bytes()

	return &Result{
		StatusCode:  resp.StatusCode,
		Header:      resp.Header.Clone(),
		Body:        out,
		ContentType: contentType,
		TokensOut:   ExtractTokensOut(out),
		OriginTier:  "dev",
	}, nil
}

func copyReplayHeaders(dst http.Header, src http.Header) {
	allowed := []string{
		"Cache-Control",
		"Content-Encoding",
		"Content-Type",
	}

	for _, k := range allowed {
		if v := src.Values(k); len(v) > 0 {
			dst.Del(k)
			for _, item := range v {
				dst.Add(k, item)
			}
		}
	}
}

func ExtractTokensOut(body []byte) int {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return 0
	}

	usage, ok := obj["usage"].(map[string]any)
	if !ok {
		return 0
	}

	for _, key := range []string{"completion_tokens", "total_tokens"} {
		v, ok := usage[key]
		if !ok {
			continue
		}

		switch x := v.(type) {
		case float64:
			if x > 0 {
				return int(x)
			}
		case int:
			if x > 0 {
				return x
			}
		}
	}

	return 0
}

func StatusText(status int) string {
	if status == 0 {
		return "upstream failed"
	}
	return fmt.Sprintf("upstream returned status %d", status)
}
