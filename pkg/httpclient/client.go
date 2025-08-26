package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	httpClient *http.Client
	timeout    time.Duration
}

func NewClient(timeout time.Duration) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		timeout:    timeout,
	}
}

func (c *Client) PostJSON(ctx context.Context, url string, payload interface{}) (*http.Response, error) {
	reqBody, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	return c.httpClient.Do(req)
}

func (c *Client) PostJSONWithTimeout(url string, payload interface{}, timeout time.Duration) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return c.PostJSON(ctx, url, payload)
}

func (c *Client) IsTimeoutError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "timeout")
}

func (c *Client) DecodeJSONResponse(resp *http.Response, v interface{}) error {
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}
