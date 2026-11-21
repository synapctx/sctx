package docker

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveImagesGroupsAndShortensDigests(t *testing.T) {
	rawBytes, err := os.ReadFile("testdata/images-many.stdout")
	if err != nil {
		t.Fatal(err)
	}
	raw := string(rawBytes)
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv: []string{"docker", "images"}, Stdout: strings.NewReader(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	t.Logf("raw=%d body=%d", len(raw), len(body))
	for _, want := range []string{
		"75 images",
		"alpine/k8s: 1.29.4 1.12GB, 1.33.13 1.26GB, 1.36.5 1.31GB",
		"alpine/socat@sha256:5ffbd6ae916c 15.8MB",
		"busybox: 1.36 6.48MB, latest 6.48MB",
		"node: 20 1.1GB, 22 1.13GB, 22-alpine 228MB, 22-slim 264MB",
		"busybox@sha256:9e2f1a8c7b3d 6.48MB",
		"+3 dangling",
		"(digests shortened to 12 hex chars = IMAGE ID)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "5ffbd6ae916cbad8") {
		t.Errorf("full digest not shortened")
	}
	// Every tag of every named image must survive grouping.
	lines := strings.Split(strings.TrimSpace(raw), "\n")[1:]
	for _, l := range lines {
		f := strings.Fields(l)
		ref, size := f[0], f[2]
		if strings.HasPrefix(ref, "<none>") {
			continue // dangling images are counted, not listed
		}
		repo, tag, ok := splitRef(ref)
		short, _ := shortDigest(ref)
		if ok {
			if !strings.Contains(body, tag+" "+size) || !strings.Contains(body, repo) {
				t.Errorf("lost %s %s", ref, size)
			}
		} else if !strings.Contains(body, short+" "+size) {
			t.Errorf("lost %s %s", ref, size)
		}
	}
	if len(body) > 2400 {
		t.Errorf("body %d bytes, want <= 2400", len(body))
	}
}

func TestGroupImages(t *testing.T) {
	cases := []struct {
		name string
		in   []imageEntry
		want []string
	}{
		{"single tag stays flat", []imageEntry{{"a/b:1", "1MB"}}, []string{"a/b:1 1MB"}},
		{"shared repo folds", []imageEntry{{"a/b:1", "1MB"}, {"c:2", "2MB"}, {"a/b:2", "3MB"}}, []string{"a/b: 1 1MB, 2 3MB", "c:2 2MB"}},
		{"digest never groups", []imageEntry{{"a@sha256:abc", "1MB"}, {"a@sha256:def", "2MB"}}, []string{"a@sha256:abc 1MB", "a@sha256:def 2MB"}},
		{"registry port is not a tag", []imageEntry{{"host:5000/x", "1MB"}}, []string{"host:5000/x 1MB"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := groupImages(tc.in)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}
