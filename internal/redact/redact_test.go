package redact

import "testing"

func TestText(t *testing.T) {
	in := "before sk-abcdefghijklmnopqrstuvwxyz and Bearer abcdefghijklmnopqrstuvwxyz after"
	got := Text(in)
	want := "before sk-REDACTED and Bearer REDACTED after"
	if got != want {
		t.Fatalf("unexpected redaction result: got %q, want %q", got, want)
	}

	plain := "no secret here"
	if Text(plain) != plain {
		t.Fatalf("plain text should remain unchanged")
	}
}

func TestEnvKey(t *testing.T) {
	if got := EnvKey("api_token", "abc"); got != "***" {
		t.Fatalf("api_token should be masked, got %q", got)
	}
	if got := EnvKey("db_password", "abc"); got != "***" {
		t.Fatalf("db_password should be masked, got %q", got)
	}
	if got := EnvKey("path", "/usr/local/bin"); got != "/usr/local/bin" {
		t.Fatalf("path should not be masked, got %q", got)
	}
}
