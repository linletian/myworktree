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

func TestControlSequenceRemnants(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "mouse event with angle bracket - NOT FILTERED (backend disabled)",
			in:   "normal text[<35;55;34Mmore text",
			want: "normal text[<35;55;34Mmore text",
		},
		{
			name: "mouse event without angle bracket - NOT FILTERED (backend disabled)",
			in:   "text[535;52;38Mend",
			want: "text[535;52;38Mend",
		},
		{
			name: "cursor position report - NOT FILTERED (backend disabled)",
			in:   "before[<35;55;34Rafter",
			want: "before[<35;55;34Rafter",
		},
		{
			name: "multiple sequences - NOT FILTERED (backend disabled)",
			in:   "[<35;55;34M[<35;55;33M[<35;54;32M",
			want: "[<35;55;34M[<35;55;33M[<35;54;32M",
		},
		{
			name: "mixed content - NOT FILTERED (backend disabled)",
			in:   "Output: [<35;55;34MHello[535;52;38M World",
			want: "Output: [<35;55;34MHello[535;52;38M World",
		},
		{
			name: "lowercase terminator - NOT FILTERED (backend disabled)",
			in:   "text[<35;55;34mmore",
			want: "text[<35;55;34mmore",
		},
		{
			name: "normal brackets preserved",
			in:   "array[0] and array[1]",
			want: "array[0] and array[1]",
		},
		{
			name: "mouse event without leading bracket - NOT FILTERED (backend disabled)",
			in:   "text35;107;1Mmore",
			want: "text35;107;1Mmore",
		},
		{
			name: "multiple mouse events without brackets - NOT FILTERED (backend disabled)",
			in:   "35;107;1M35;106;2M35;105;3M",
			want: "35;107;1M35;106;2M35;105;3M",
		},
		{
			name: "SGR mouse with single digit button - NOT FILTERED (backend disabled)",
			in:   "[<0;100;50M",
			want: "[<0;100;50M",
		},
		{
			name: "cursor position report - NOT FILTERED (backend disabled)",
			in:   "[35;107R",
			want: "[35;107R",
		},
		{
			name: "cursor report without bracket - NOT FILTERED (backend disabled)",
			in:   "35;107R",
			want: "35;107R",
		},
		{
			name: "scroll wheel events - NOT FILTERED (backend disabled)",
			in:   "64;80;24M65;80;24M",
			want: "64;80;24M65;80;24M",
		},
		{
			name: "mixed with text - NOT FILTERED (backend disabled)",
			in:   "Error: 35;107;1M occurred",
			want: "Error: 35;107;1M occurred",
		},
		{
			name: "RGB color (no terminator)",
			in:   "color: [255;0;0]",
			want: "color: [255;0;0]",
		},
		{
			name: "small ambiguous coords (preserve)",
			in:   "pos: [0;5;9M",
			want: "pos: [0;5;9M",
		},
		{
			name: "JSON array",
			in:   `{"data": [100, 200]}`,
			want: `{"data": [100, 200]}`,
		},
		{
			name: "CSS rgb notation",
			in:   "background: rgb(255, 0, 0)",
			want: "background: rgb(255, 0, 0)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Text(tt.in)
			if got != tt.want {
				t.Errorf("Text(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
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
