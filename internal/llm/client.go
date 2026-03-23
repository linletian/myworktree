package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

const (
	timeout = 10 * time.Second
)

// debugLLM controls verbose request/response logging. Set LLM_DEBUG=1 to enable.
var debugLLM = os.Getenv("LLM_DEBUG") == "1"

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

func callOpenAI(ctx context.Context, apiKey, url, model, prompt string) (string, error) {
	reqBody := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens":      1024,
		"temperature":     0.3,
		"reasoning_split": true, // Separate thinking from final answer
	}

	body, status, err := doHTTPRequest(ctx, url, apiKey, "openai", reqBody)
	if err != nil {
		return "", err
	}

	if status == 401 {
		return "", fmt.Errorf("%w: OpenAI API returned 401", ErrInvalidAPIKey)
	}
	if status != http.StatusOK {
		return "", &ErrHTTPError{StatusCode: status}
	}

	// With reasoning_split: true, content field contains the final answer only
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("no choices in response")
	}
	return resp.Choices[0].Message.Content, nil
}

func callAnthropic(ctx context.Context, apiKey, url, model, prompt string) (string, error) {
	reqBody := map[string]any{
		"model":      model,
		"max_tokens": 1024,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	body, status, err := doHTTPRequest(ctx, url, apiKey, "anthropic", reqBody)
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

func doHTTPRequest(ctx context.Context, url, apiKey, authType string, reqBody map[string]any) ([]byte, int, error) {
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, 0, err
	}

	if debugLLM {
		// Pretty-print request body (mask API key)
		var maskedBody map[string]any
		_ = json.Unmarshal(jsonBody, &maskedBody)
		if m, ok := maskedBody["extra_body"].(map[string]any); ok {
			delete(m, "api_key") // extra_body won't have api_key, but just in case
		}
		maskedJSON, _ := json.MarshalIndent(maskedBody, "", "  ")
		log.Printf("[LLM DEBUG] --> POST %s\n%s", url, maskedJSON)
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	switch authType {
	case "openai":
		req.Header.Set("Authorization", "Bearer "+apiKey)
	case "anthropic":
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

	if debugLLM {
		log.Printf("[LLM DEBUG] <-- %d\n%s", resp.StatusCode, string(body))
	}

	return body, resp.StatusCode, nil
}
