// Package llm wraps OpenRouter for query generation and tool documentation.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a minimal OpenRouter chat completion client.
type Client struct {
	apiKey  string
	model   string
	baseURL string
	http    *http.Client
}

func New(apiKey, model, baseURL string, timeout time.Duration) *Client {
	return &Client{
		apiKey:  apiKey,
		model:   model,
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: timeout},
	}
}

// Model returns the configured model name (safe to expose to admins).
func (c *Client) Model() string {
	if c == nil {
		return ""
	}
	return c.model
}

// BaseURL returns the configured API base URL (safe to expose to admins).
func (c *Client) BaseURL() string {
	if c == nil {
		return ""
	}
	return c.baseURL
}

// Message is a single chat turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatReq struct {
	Model          string    `json:"model"`
	Messages       []Message `json:"messages"`
	ResponseFormat *respFmt  `json:"response_format,omitempty"`
	Temperature    float64   `json:"temperature"`
}

type respFmt struct {
	Type string `json:"type"`
}

type chatResp struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Chat issues a non-streaming chat completion. When jsonObject is true, the
// model is instructed to respond with strict JSON.
func (c *Client) Chat(ctx context.Context, msgs []Message, jsonObject bool) (string, error) {
	if c.apiKey == "" {
		return "", errors.New("OPENROUTER_API_KEY not set")
	}
	body := chatReq{
		Model:       c.model,
		Messages:    msgs,
		Temperature: 0.1,
	}
	if jsonObject {
		body.ResponseFormat = &respFmt{Type: "json_object"}
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://contextforge.local")
	req.Header.Set("X-Title", "ContextForge")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("openrouter %d: %s", resp.StatusCode, truncate(string(raw), 512))
	}
	var cr chatResp
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", fmt.Errorf("openrouter decode: %w", err)
	}
	if cr.Error != nil {
		return "", fmt.Errorf("openrouter: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", errors.New("openrouter: empty response")
	}
	return cr.Choices[0].Message.Content, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
