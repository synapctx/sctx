package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const buildKitPlainFixture = "#1 [internal] load build definition from Dockerfile\n" +
	"#1 transferring dockerfile: 215B done\n" +
	"#1 DONE 0.0s\n" +
	"\n" +
	"#2 [internal] load metadata for docker.io/library/node:18\n" +
	"#2 DONE 1.2s\n" +
	"\n" +
	"#3 [1/3] FROM docker.io/library/node:18@sha256:abcdef1234567890\n" +
	"#3 CACHED\n" +
	"\n" +
	"#4 [2/3] RUN npm install\n" +
	"#4 0.523 added 120 packages in 5s\n" +
	"#4 DONE 12.3s\n" +
	"\n" +
	"#5 [3/3] COPY . .\n" +
	"#5 DONE 0.5s\n" +
	"\n" +
	"#6 exporting to image\n" +
	"#6 exporting layers done\n" +
	"#6 writing image sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab done\n" +
	"#6 naming to docker.io/library/myapp:latest done\n" +
	"#6 DONE 0.1s\n"

const buildKitArrowFixture = "[+] Building 12.3s (11/11) FINISHED\n" +
	" => [internal] load build definition from Dockerfile        0.0s\n" +
	" => => transferring dockerfile: 215B                        0.0s\n" +
	" => [internal] load metadata for docker.io/library/node:18  1.2s\n" +
	" => [1/3] FROM docker.io/library/node:18@sha256:abc          0.0s\n" +
	" => CACHED [2/3] RUN npm install                              12.1s\n" +
	" => [3/3] COPY . .                                             0.1s\n" +
	" => exporting to image                                         0.2s\n" +
	" => => exporting layers                                        0.1s\n" +
	" => => writing image sha256:abcdef1234567890abcdef1234567890abcdef      0.0s\n" +
	" => => naming to docker.io/library/myapp:latest                0.0s\n"

const legacyBuildFixture = "Step 1/4 : FROM node:18\n" +
	" ---> abcdef123456\n" +
	"Step 2/4 : WORKDIR /app\n" +
	" ---> Using cache\n" +
	" ---> 123456abcdef\n" +
	"Step 3/4 : RUN npm install\n" +
	" ---> Running in 1234567890ab\n" +
	"added 120 packages in 5s\n" +
	"Removing intermediate container 1234567890ab\n" +
	" ---> def456abc789\n" +
	"Step 4/4 : COPY . .\n" +
	" ---> 789abc123def\n" +
	"Successfully built 789abc123def\n" +
	"Successfully tagged myapp:latest\n"

func TestAggressiveBuild(t *testing.T) {
	f := New()

	t.Run("buildkit plain progress collapses to compact steps", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "build", "-t", "myapp", "."}, Stdout: strings.NewReader(buildKitPlainFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		for _, want := range []string{
			"6 build steps",
			"#4 [2/3] RUN npm install done 12.3s",
			"#3 [1/3] FROM docker.io/library/node:18@sha256:abcdef1234567890 cached",
			"writing image sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab done",
			"naming to docker.io/library/myapp:latest done",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
		if strings.Contains(body, "0.523 added 120 packages") {
			t.Errorf("body still contains raw RUN progress noise: %q", body)
		}
		if len(out.Body) >= len(buildKitPlainFixture) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(buildKitPlainFixture))
		}
	})

	t.Run("buildkit fancy arrow progress collapses to compact steps", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "build", "-t", "myapp", "."}, Stdout: strings.NewReader(buildKitArrowFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		for _, want := range []string{
			"CACHED [2/3] RUN npm install",
			"writing image sha256:abcdef1234567890abcdef1234567890abcdef",
			"naming to docker.io/library/myapp:latest",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
		if strings.Contains(body, "transferring dockerfile") {
			t.Errorf("body still contains nested transfer progress: %q", body)
		}
		if len(out.Body) >= len(buildKitArrowFixture) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(buildKitArrowFixture))
		}
	})

	t.Run("legacy step progress collapses, dropping intermediate container noise", func(t *testing.T) {
		in := format.Input{Argv: []string{"docker", "build", "-t", "myapp", "."}, Stdout: strings.NewReader(legacyBuildFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		for _, want := range []string{
			"4 build steps",
			"Step 3/4 : RUN npm install",
			"Successfully built 789abc123def",
			"Successfully tagged myapp:latest",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
		if strings.Contains(body, "Removing intermediate container") || strings.Contains(body, "--->") {
			t.Errorf("body still contains layer-id noise: %q", body)
		}
	})

	t.Run("non-zero exit degrades and preserves ERROR verbatim via relaxed", func(t *testing.T) {
		failed := buildKitPlainFixture + "#4 ERROR: process \"/bin/sh -c npm install\" did not complete successfully: exit code 1\n"
		f := New()
		in := format.Input{Argv: []string{"docker", "build", "-t", "myapp", "."}, Stdout: strings.NewReader(failed), ExitCode: 1}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable", err)
		}

		in2 := format.Input{Argv: []string{"docker", "build", "-t", "myapp", "."}, Stdout: strings.NewReader(failed), ExitCode: 1}
		out, err := f.Relaxed(context.Background(), in2)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		if !strings.Contains(string(out.Body), "ERROR: process") {
			t.Errorf("relaxed body dropped ERROR line: %q", out.Body)
		}
	})
}
