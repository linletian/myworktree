package pty

import (
	"strings"
	"testing"

	"myworktree/internal/framework"
)

func TestDriver_Manifest(t *testing.T) {
	d := Driver{}
	m := d.Manifest()
	if m.Name != "pty" {
		t.Fatalf("Name = %q, want pty", m.Name)
	}
	if !m.Interactive {
		t.Fatalf("Interactive = false, want true for PTY kind")
	}
	if m.Label == "" || m.Description == "" {
		t.Fatalf("Label/Description must be non-empty: %+v", m)
	}
}

func TestDriver_HTTPHint(t *testing.T) {
	d := Driver{}
	if hint := d.HTTPHint("any-id"); hint != "" {
		t.Fatalf("PTY kind must not register HTTP routes; got hint %q", hint)
	}
}

func TestDriver_KindBlob_Empty(t *testing.T) {
	d := Driver{}
	h := framework.NewHandle("pty", &Handle{})
	blob, err := d.KindBlob(h)
	if err != nil {
		t.Fatalf("KindBlob: %v", err)
	}
	// PTY has no kind-private state; blob is the empty marker {}.
	if !strings.HasPrefix(string(blob), "{") {
		t.Fatalf("PTY blob = %s, want object", blob)
	}
}

func TestNewID_KindName(t *testing.T) {
	h := framework.NewHandle("pty", &Handle{})
	if h.KindName != "pty" {
		t.Fatalf("Handle.KindName = %q, want pty", h.KindName)
	}
}

func TestMustHandle_PanicsOnWrongType(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("mustHandle should panic on wrong inner type")
		}
	}()
	mustHandle(framework.NewHandle("pty", "not a *Handle"))
}
