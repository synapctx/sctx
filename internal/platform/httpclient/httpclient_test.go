package httpclient

import (
	"runtime"
	"testing"
)

func TestUserAgent(t *testing.T) {
	got := UserAgent("1.2.3", "claude-code")
	want := "sctx/1.2.3 (" + runtime.GOOS + "/" + runtime.GOARCH + "; client=claude-code)"
	if got != want {
		t.Errorf("UserAgent = %q, want %q", got, want)
	}
}

func TestUserAgentDefaultsEmptyFields(t *testing.T) {
	got := UserAgent("", "")
	want := "sctx/dev (" + runtime.GOOS + "/" + runtime.GOARCH + "; client=unknown)"
	if got != want {
		t.Errorf("UserAgent(\"\", \"\") = %q, want %q", got, want)
	}
}
