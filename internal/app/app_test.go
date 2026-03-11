package app

import "testing"

func TestParseInt64Default(t *testing.T) {
	if got := parseInt64Default("42", -1); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
	if got := parseInt64Default("  ", -1); got != -1 {
		t.Fatalf("blank input should return default, got %d", got)
	}
	if got := parseInt64Default("abc", -1); got != -1 {
		t.Fatalf("invalid input should return default, got %d", got)
	}
}

func TestNormalizeLabels(t *testing.T) {
	if got := normalizeLabels(nil); got != nil {
		t.Fatalf("nil input should return nil, got %#v", got)
	}
	if got := normalizeLabels(map[string]string{}); got != nil {
		t.Fatalf("empty map should return nil, got %#v", got)
	}

	got := normalizeLabels(map[string]string{
		" team ":  " backend ",
		"":        "x",
		"x":       "",
		"owner":   "   ",
		"   ":     "value",
		"service": " api ",
	})
	if len(got) != 2 || got["team"] != "backend" || got["service"] != "api" {
		t.Fatalf("unexpected labels normalization result: %#v", got)
	}
	if _, ok := got["owner"]; ok {
		t.Fatalf("owner with whitespace value should be dropped: %#v", got)
	}
	if _, ok := got["x"]; ok {
		t.Fatalf("label with empty value should be dropped: %#v", got)
	}
	if _, ok := got[""]; ok {
		t.Fatalf("empty key should be dropped: %#v", got)
	}

	got = normalizeLabels(map[string]string{
		" ":   " ",
		"":    "",
		"foo": "   ",
	})
	if got != nil {
		t.Fatalf("all invalid labels should return nil, got %#v", got)
	}
}

func TestClientIP(t *testing.T) {
	if got := clientIP("127.0.0.1:12345"); got != "127.0.0.1" {
		t.Fatalf("expected host only, got %q", got)
	}
	if got := clientIP("not-a-host-port"); got != "not-a-host-port" {
		t.Fatalf("invalid host:port should return original, got %q", got)
	}
}
