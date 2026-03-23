package llm

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Prompt is the prompt sent to the LLM for generating a branch name.
const Prompt = `You are a git branch naming expert. Convert the following task description into a concise, standard git branch name.

Requirements:
1. Maximum 100 characters
2. Only lowercase letters, numbers, and hyphens
3. Must start with a letter (git convention)
4. Semantic and clear
5. Use English, may include numbers

Task description: %s

Respond with only the branch name, no explanation.`

// branchNameRegex validates the format of a generated branch name.
var branchNameRegex = regexp.MustCompile(`^[a-z]([a-z0-9-]*[a-z0-9])?$`)

// BuildPrompt formats the prompt with the given task description.
func BuildPrompt(taskDesc string) string {
	return fmt.Sprintf(Prompt, taskDesc)
}

// GenerateBranchName generates a branch name from a task description using the
// configured LLM provider. It returns an error if the mode is "regex" or if the
// LLM call fails.
func GenerateBranchName(ctx context.Context, taskDesc string) (string, error) {
	cfg := Load()
	switch cfg.Mode {
	case "regex":
		return "", errors.New("LLM mode is 'regex', cannot generate branch name")
	case "openai":
		return callOpenAI(ctx, cfg.APIKey, taskDesc)
	case "anthropic":
		return callAnthropic(ctx, cfg.APIKey, taskDesc)
	default:
		return "", fmt.Errorf("unsupported LLM mode: %s", cfg.Mode)
	}
}

// TestConnection tests whether the configured LLM API key is valid by sending
// a simple test request.
func TestConnection(ctx context.Context) (string, error) {
	return GenerateBranchName(ctx, "test")
}

// parseBranchName extracts and cleans the branch name from the LLM response.
// It removes markdown formatting, quotes, and extra whitespace.
func parseBranchName(response string) (string, error) {
	// Remove markdown code fences
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")

	// Remove leading/trailing whitespace
	response = strings.TrimSpace(response)

	// Remove surrounding quotes (single or double)
	response = strings.Trim(response, "\"'")

	// Take the first line if there are multiple
	if idx := strings.Index(response, "\n"); idx != -1 {
		response = response[:idx]
	}

	// Validate format
	response = strings.TrimSpace(response)
	if response == "" {
		return "", errors.New("empty branch name returned")
	}
	if !branchNameRegex.MatchString(response) {
		// Try to clean it up - remove invalid characters
		cleaned := cleanBranchName(response)
		if cleaned == "" || !branchNameRegex.MatchString(cleaned) {
			return "", fmt.Errorf("invalid branch name format: %s", response)
		}
		return cleaned, nil
	}
	return response, nil
}

// cleanBranchName attempts to convert a response into a valid branch name
// by removing invalid characters and trimming.
func cleanBranchName(s string) string {
	// Replace non-alphanumeric-hyphen chars with hyphens
	var result strings.Builder
	for i, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			result.WriteRune(r)
		} else if r == '-' || r == ' ' || r == '_' {
			// Collapse consecutive hyphens
			if i > 0 && result.Len() > 0 && result.String()[result.Len()-1] != '-' {
				result.WriteByte('-')
			}
		}
	}
	s = result.String()
	s = strings.Trim(s, "-")
	// Truncate to 100 chars
	if len(s) > 100 {
		s = s[:100]
		s = strings.Trim(s, "-")
	}
	// Ensure it starts with a letter
	if len(s) > 0 && (s[0] < 'a' || s[0] > 'z') {
		// Remove leading non-letters
		for len(s) > 0 && (s[0] < 'a' || s[0] > 'z') {
			s = s[1:]
		}
	}
	return s
}
