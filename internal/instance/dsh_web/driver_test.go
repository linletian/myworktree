package dsh_web

import (
	"strings"
	"testing"
)

func TestExtractListeningAddress(t *testing.T) {
	cases := []struct {
		in    string
		host  string
		port  string
		token string
		ok    bool
	}{
		{"dsh web: http://127.0.0.1:4096", "127.0.0.1", "4096", "", true},
		{"dsh web: http://127.0.0.1:4096\r\n", "127.0.0.1", "4096", "", true},
		{"dsh web: http://127.0.0.1:4096   ", "127.0.0.1", "4096", "", true},
		{"dsh web: http://127.0.0.1:4096 (LAN: http://192.168.1.5:4096)", "127.0.0.1", "4096", "", true},
		{"\x1b[32mdsh web: http://127.0.0.1:4096\x1b[0m", "127.0.0.1", "4096", "", true}, // ANSI
		{"dsh web: http://localhost:4096", "localhost", "4096", "", true},
		// dsh >=0.1.2 ready lines carry the browser-auth token.
		{"dsh web: http://127.0.0.1:41143/?token=abc-DEF_123", "127.0.0.1", "41143", "abc-DEF_123", true},
		{"dsh web: http://127.0.0.1:41143/?token=abc-DEF_123 (LAN: http://100.86.87.23:41143/?token=zzz)", "127.0.0.1", "41143", "abc-DEF_123", true},
		{"\x1b[32mdsh web: http://127.0.0.1:41143/?token=abc-DEF_123\x1b[0m (LAN: http://100.86.87.23:41143/?token=zzz)", "127.0.0.1", "41143", "abc-DEF_123", true}, // ANSI + LAN
		{"some other log line", "", "", "", false},
		{"", "", "", "", false},
		{"dsh web: not a url", "", "", "", false},
	}
	for _, c := range cases {
		host, port, token, ok := extractListeningAddress(c.in)
		if host != c.host || port != c.port || token != c.token || ok != c.ok {
			t.Errorf("extractListeningAddress(%q) = (%q, %q, %q, %v), want (%q, %q, %q, %v)",
				c.in, host, port, token, ok, c.host, c.port, c.token, c.ok)
		}
	}
}

func TestBuildEnv(t *testing.T) {
	// Tag env overrides inherited env; everything else passes through.
	t.Setenv("DSH_MW_TEST_KEEP", "inherited")
	env := buildEnv(map[string]string{
		"DSH_MW_TEST_KEEP": "overridden",
		"DSH_MW_TEST_NEW":  "tagged",
	})
	var keep, neu string
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "DSH_MW_TEST_KEEP":
			keep = v
		case "DSH_MW_TEST_NEW":
			neu = v
		}
	}
	if keep != "overridden" {
		t.Errorf("tag override = %q, want overridden", keep)
	}
	if neu != "tagged" {
		t.Errorf("tag env = %q, want tagged", neu)
	}

	// nil tag env is safe and keeps inherited vars.
	env2 := buildEnv(nil)
	found := false
	for _, kv := range env2 {
		if strings.HasPrefix(kv, "DSH_MW_TEST_KEEP=") {
			found = true
		}
	}
	if !found {
		t.Error("buildEnv(nil) dropped inherited variable")
	}
}
