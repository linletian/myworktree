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

const (
	timeout        = 10 * time.Second
	openAIModel    = "gpt-4o-mini"
	anthropicModel = "claude-3-5-haiku"
)

// ErrInvalidAPIKey indicates the API key is invalid (401 from the provider).
var ErrInvalidAPIKey = errors.New("invalid API key")

// ErrNetworkError indicates a network or timeout error.
var ErrNetworkError = errors.New("network error or timeout")

// ErrHTTPError wraps an HTTP error response (non-200 status code).
type ErrHTTPError struct {
	StatusCode int
}

func (e *ErrHTTPError) Error() string {
	return fmt.Sprintf("HTTP error: status %d", e.StatusCode)
}

func (e *ErrHTTPError) Is(target error) bool {
	return target == ErrNetworkError // for backward compatibility with callers checking errors.Is(ErrNetworkError)
}

func callOpenAI(ctx context.Context, apiKey, prompt string) (string, error) {
	reqBody := map[string]any{
		"model": openAIModel,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens":  50,
		"temperature": 0.3,
	}

	body, status, err := doHTTPRequest(ctx, "https://api.openai.com/v1/chat/completions", apiKey, reqBody)
	if err != nil {
		return "", err
	}

	if status == 401 {
		return "", fmt.Errorf("%w: OpenAI API returned 401", ErrInvalidAPIKey)
	}
	if status != http.StatusOK {
		return "", &ErrHTTPError{StatusCode: status}
	}

	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("failed to parse OpenAI response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("OpenAI returned no choices")
	}
	return resp.Choices[0].Message.Content, nil
}

func callAnthropic(ctx context.Context, apiKey, prompt string) (string, error) {
	reqBody := map[string]any{
		"model":      anthropicModel,
		"max_tokens": 50,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	body, status, err := doHTTPRequest(ctx, "https://api.anthropic.com/v1/messages", apiKey, reqBody)
	if err != nil {
		return "", err
	}

	if status == 401 {
		return "", fmt.Errorf("%w: Anthropic API returned 401", ErrInvalidAPIKey)
	}
	if status != http.StatusOK {
		return "", &ErrHTTPError{StatusCode: status}
	}

	var resp struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("failed to parse Anthropic response: %w", err)
	}
	if len(resp.Content) == 0 {
		return "", errors.New("Anthropic returned no content")
	}
	return resp.Content[0].Text, nil
}

func doHTTPRequest(ctx context.Context, url, apiKey string, reqBody map[string]any) ([]byte, int, error) {
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, 0, err
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	switch {
	case strings.Contains(url, "openai.com"):
		req.Header.Set("Authorization", "Bearer "+apiKey)
	case strings.Contains(url, "anthropic.com"):
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, 0, fmt.Errorf("%w: request timed out after %v", ErrNetworkError, timeout)
		}
		return nil, 0, fmt.Errorf("%w: %v", ErrNetworkError, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response body: %w", err)
	}
	return body, resp.StatusCode, nil
}
