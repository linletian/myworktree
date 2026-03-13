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
			name: "mouse event with angle bracket",
			in:   "normal text[<35;55;34Mmore text",
			want: "normal textmore text",
		},
		{
			name: "mouse event without angle bracket",
			in:   "text[535;52;38Mend",
			want: "textend",
		},
		{
			name: "cursor position report",
			in:   "before[<35;55;34Rafter",
			want: "beforeafter",
		},
		{
			name: "multiple sequences",
			in:   "[<35;55;34M[<35;55;33M[<35;54;32M",
			want: "",
		},
		{
			name: "mixed content",
			in:   "Output: [<35;55;34MHello[535;52;38M World",
			want: "Output: Hello World",
		},
		{
			name: "lowercase terminator",
			in:   "text[<35;55;34mmore",
			want: "textmore",
		},
		{
			name: "normal brackets preserved",
			in:   "array[0] and array[1]",
			want: "array[0] and array[1]",
		},
		{
			name: "mouse event without leading bracket",
			in:   "text35;107;1Mmore",
			want: "textmore",
		},
		{
			name: "multiple mouse events without brackets",
			in:   "35;107;1M35;106;2M35;105;3M",
			want: "",
		},
		{
			name: "SGR mouse with single digit button",
			in:   "[<0;100;50M",
			want: "",
		},
		{
			name: "cursor position report",
			in:   "[35;107R",
			want: "",
		},
		{
			name: "cursor report without bracket",
			in:   "35;107R",
			want: "",
		},
		{
			name: "scroll wheel events",
			in:   "64;80;24M65;80;24M",
			want: "",
		},
		{
			name: "mixed with text",
			in:   "Error: 35;107;1M occurred",
			want: "Error:  occurred",
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
