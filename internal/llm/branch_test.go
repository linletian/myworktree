package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBuildPrompt(t *testing.T) {
	prompt := BuildPrompt("fix login bug")
	if !strings.Contains(prompt, "fix login bug") {
		t.Errorf("prompt should contain task description, got: %s", prompt)
	}
	if !strings.Contains(prompt, "Maximum 100 characters") {
		t.Errorf("prompt should contain requirements, got: %s", prompt)
	}
	if !strings.Contains(prompt, "lowercase") {
		t.Errorf("prompt should mention lowercase requirement, got: %s", prompt)
	}
}

func TestPromptFormatting(t *testing.T) {
	tests := []struct {
		input    string
		wantCont string
	}{
		{"fix login", "fix login"},
		{"Feature: Add dark mode", "Feature: Add dark mode"},
		{"bug in user auth flow", "bug in user auth flow"},
	}

	for _, tt := range tests {
		prompt := BuildPrompt(tt.input)
		if !strings.Contains(prompt, tt.wantCont) {
			t.Errorf("BuildPrompt(%q) should contain %q, got: %s", tt.input, tt.wantCont, prompt)
		}
	}
}

func TestParseBranchName(t *testing.T) {
	tests := []struct {
		input    string
		want     string
		wantErr  bool
	}{
		{"feature-auth", "feature-auth", false},
		{"fix-login-bug", "fix-login-bug", false},
		{"test123", "test123", false},
		{"a", "a", false},
		// Markdown code fences
		{"```feature-auth```", "feature-auth", false},
		{"```\nfix-login\n```", "fix-login", false},
		// Quotes
		{`"feature-auth"`, "feature-auth", false},
		{"'fix-login'", "fix-login", false},
		// Multi-line (takes first line)
		{"feature-auth\nanother-line", "feature-auth", false},
		// Empty
		{"", "", true},
		{"   ", "", true},
		// Whitespace
		{"  feature-auth  ", "feature-auth", false},
	}

	for _, tt := range tests {
		got, err := parseBranchName(tt.input)
		if tt.wantErr && err == nil {
			t.Errorf("parseBranchName(%q) expected error, got nil", tt.input)
			continue
		}
		if !tt.wantErr && err != nil {
			t.Errorf("parseBranchName(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("parseBranchName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCleanBranchName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"feature_auth", "feature-auth"},
		{"Feature Auth", "feature-auth"},
		{"fix--login", "fix-login"},
		{"a-b-c", "a-b-c"},
		{"123-test", "test"}, // starts with number, stripped
		{"FEATURE", "feature"},
		{"fix_login_bug_123", "fix-login-bug-123"},
		{"---test---", "test"},
		{"a", "a"},
		{"", ""},
	}

	for _, tt := range tests {
		got := cleanBranchName(tt.input)
		if got != tt.want {
			t.Errorf("cleanBranchName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCleanBranchNameLength(t *testing.T) {
	// Test that we truncate to 100 chars
	long := strings.Repeat("a", 150)
	got := cleanBranchName(long)
	if len(got) > 100 {
		t.Errorf("cleanBranchName should truncate to 100 chars, got %d: %q", len(got), got)
	}
}

func TestBranchNameRegex(t *testing.T) {
	valid := []string{
		"a",
		"feature-auth",
		"fix-login-bug-123",
		strings.Repeat("a", 100),
		"a1",
	}
	invalid := []string{
		"",
		"Feature",       // uppercase
		"feature_auth",  // underscore
		"feature auth",  // space
		"-feature",      // starts with hyphen
		"feature-",     // ends with hyphen
		"123test",      // starts with number
	}

	for _, s := range valid {
		if !branchNameRegex.MatchString(s) {
			t.Errorf("branchNameRegex should match %q", s)
		}
	}
	for _, s := range invalid {
		if branchNameRegex.MatchString(s) {
			t.Errorf("branchNameRegex should NOT match %q", s)
		}
	}
}

func TestGenerateBranchNameRegexMode(t *testing.T) {
	// Save original config
	orig := Load()
	defer Save(orig)

	// Set regex mode
	Save(&Config{Mode: "regex", APIKey: ""})

	_, err := GenerateBranchName(context.Background(), "test task")
	if err == nil {
		t.Errorf("GenerateBranchName with regex mode should return error")
	}
}

func TestGenerateBranchNameInvalidMode(t *testing.T) {
	// Save original config
	orig := Load()
	defer Save(orig)

	// Set invalid mode
	Save(&Config{Mode: "invalid", APIKey: "sk-test"})

	_, err := GenerateBranchName(context.Background(), "test task")
	if err == nil {
		t.Errorf("GenerateBranchName with invalid mode should return error")
	}
}

func TestGenerateBranchNameNoAPIKey(t *testing.T) {
	// Save original config
	orig := Load()
	defer Save(orig)

	// Set openai mode but no API key
	Save(&Config{Mode: "openai", APIKey: ""})

	_, err := GenerateBranchName(context.Background(), "test task")
	// Should fail because there's no API key configured (or rather, empty key)
	// The actual error comes from the HTTP request
	if err == nil {
		t.Errorf("GenerateBranchName with empty API key should return error")
	}
}

func TestMaskKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"sk-abc123xyz", "sk-***xyz"},
		{"sk-abc", "sk-***abc"},  // exactly 6 chars
		{"sk-123456", "sk-***456"}, // exactly 6 chars
		{"abc", "***"},      // < 6 chars
		{"ab", "***"},       // < 6 chars
		{"abcdef", "abc***def"}, // exactly 6 chars
		{"", "***"},        // empty
	}

	for _, tt := range tests {
		got := MaskKey(tt.key)
		if got != tt.want {
			t.Errorf("MaskKey(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestErrIs(t *testing.T) {
	// Test that our sentinel errors can be checked with errors.Is
	err := ErrInvalidAPIKey
	if !errors.Is(err, ErrInvalidAPIKey) {
		t.Errorf("ErrInvalidAPIKey should match itself")
	}
}

func TestErrHTTPError(t *testing.T) {
	// ErrHTTPError should match ErrNetworkError via Is() for backward compat
	httpErr := &ErrHTTPError{StatusCode: 400}
	if !errors.Is(httpErr, ErrNetworkError) {
		t.Errorf("ErrHTTPError should match ErrNetworkError for backward compat")
	}
	if httpErr.Error() != "HTTP error: status 400" {
		t.Errorf("Error() = %q, want %q", httpErr.Error(), "HTTP error: status 400")
	}
}
