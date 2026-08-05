package gotest

import (
	"context"
	"strings"
	"testing"
)

const getFetchAndUpgradeStderr = "go: downloading example.com/foo v1.3.0\n" +
	"go: downloading example.com/bar v0.2.0\n" +
	"go: upgraded example.com/foo v1.2.3 => v1.3.0\n" +
	"go: added example.com/baz v0.1.0\n"

const getNotFoundStderr = "go: example.com/nope@latest: module example.com/nope: git ls-remote -q origin: exit status 128\n"

func TestAggressive_Get(t *testing.T) {
	f := New()

	t.Run("fetch noise collapses, version changes kept", func(t *testing.T) {
		in := newInput([]string{"go", "get", "example.com/foo@latest"}, "go get", "", getFetchAndUpgradeStderr, 0, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "…+2 modules fetched") {
			t.Errorf("Body = %q, want fetch noise collapsed to a count", body)
		}
		if !strings.Contains(body, "go: upgraded example.com/foo v1.2.3 => v1.3.0") {
			t.Errorf("Body = %q, want the upgrade line preserved", body)
		}
		if !strings.Contains(body, "go: added example.com/baz v0.1.0") {
			t.Errorf("Body = %q, want the added-module line preserved", body)
		}
		if strings.Contains(body, "go: downloading") {
			t.Errorf("Body = %q, should not contain raw download noise", body)
		}
	})

	t.Run("module-not-found error preserved verbatim", func(t *testing.T) {
		in := newInput([]string{"go", "get", "example.com/nope@latest"}, "go get", "", getNotFoundStderr, 1, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !strings.Contains(string(rendered.Body), "git ls-remote -q origin: exit status 128") {
			t.Errorf("Body = %q, want the module-not-found diagnostic preserved", string(rendered.Body))
		}
	})

	t.Run("version-change list over the cap collapses to a count", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < maxGetChanges+5; i++ {
			b.WriteString("go: upgraded example.com/dep v1.0.0 => v1.0.1\n")
		}
		in := newInput([]string{"go", "get", "-u", "./..."}, "go get", "", b.String(), 0, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "…+5 more version changes") {
			t.Errorf("Body = %q, want a capped version-changes marker", body)
		}
	})
}
