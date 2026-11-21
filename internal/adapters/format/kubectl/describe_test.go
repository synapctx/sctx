package kubectl

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const kubectlDescribeFixture = `Name:             web-7d8f9c6b5d-abcde
Namespace:        default
Priority:         0
Node:             node-1/10.0.0.5
Start Time:       Mon, 01 Jan 2024 00:00:00 +0000
Labels:           app=web
                  pod-template-hash=7d8f9c6b5d
Annotations:      <none>
Status:           Running
IP:               10.1.2.3
IPs:
  IP:  10.1.2.3
Controlled By:    ReplicaSet/web-7d8f9c6b5d
Containers:
  web:
    Container ID:  containerd://abc123
    Image:         nginx:1.25
    Image ID:      docker.io/library/nginx@sha256:deadbeef
    Port:          80/TCP
    Host Port:     0/TCP
    State:         Running
      Started:     Mon, 01 Jan 2024 00:00:00 +0000
    Ready:         True
    Restart Count: 0
Conditions:
  Type              Status
  Initialized       True
  Ready             True
Volumes:
  kube-api-access:
    Type:  Projected
QoS Class:                   BestEffort
Node-Selectors:              <none>
Tolerations:                 node.kubernetes.io/not-ready:NoExecute op=Exists for 300s
Events:
  Type    Reason     Age   From               Message
  ----    ------     ----  ----               -------
  Normal  Scheduled  5m    default-scheduler  Successfully assigned default/web-7d8f9c6b5d-abcde to node-1
  Normal  Pulled     5m    kubelet            Container image already present on machine
  Normal  Created    5m    kubelet            Created container web
  Normal  Started    5m    kubelet            Started container web
`

func TestDescribeElidesBoilerplateAndKeepsDiagnostics(t *testing.T) {
	in := format.Input{Argv: []string{"kubectl", "describe", "pod", "web-7d8f9c6b5d-abcde"}, Stdout: strings.NewReader(kubectlDescribeFixture)}
	out, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{"Image:         nginx:1.25", "State:         Running", "Restart Count: 0", "Ready", "…+"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	for _, gone := range []string{"Container ID:  containerd://abc123", "Image ID:", "Node-Selectors:"} {
		if strings.Contains(body, gone) {
			t.Errorf("body still holds %q", gone)
		}
	}
	if !strings.Contains(body, "elided (") || !strings.Contains(body, "Container ID") {
		t.Errorf("single-line drops are not announced by name: %s", body)
	}
}

func TestDescribeOnlyCollapsesExactRuns(t *testing.T) {
	raw := kubectlDescribeFixture + "Repeated diagnostic\nRepeated diagnostic\nRepeated diagnostic\n"
	in := format.Input{Argv: []string{"kubectl", "describe", "pod", "web"}, Stdout: strings.NewReader(raw)}
	out, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{"Image:         nginx:1.25", "Repeated diagnostic ×3"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestDescribeRealFixtures(t *testing.T) {
	tests := []struct {
		file string
		args []string
		keep []string // diagnostic lines that must survive verbatim
		gone []string
	}{
		{"describe-pod-crashloop.txt", []string{"describe", "pod", "crasher"},
			[]string{"Exit Code:    3", "Restart Count:  3", "Reason:       Error", "Warning  BackOff", "ContainersReady             False", "Image:         busybox:1.36"},
			[]string{"Container ID:", "Image ID:", "kube-api-access-4k4bm (ro)", "Projected (a volume"}},
		{"describe-pod-imagepull.txt", []string{"describe", "pod", "badimage"},
			[]string{"ErrImagePull", "ImagePullBackOff", "no such host", "registry.invalid/nope:latest"},
			[]string{"Projected (a volume"}},
		{"describe-node.txt", []string{"describe", "node", "sctxcov3-control-plane"},
			[]string{"KubeletReady", "Allocatable: same as Capacity", "Kubelet Version:            v1.37.0", "Non-terminated Pods:", "Taints:             <none>"},
			[]string{"Machine ID:", "hugepages-1Gi"}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			raw := nativeFixture(t, tt.file)
			in := format.Input{Argv: append([]string{"kubectl"}, tt.args...), Stdout: strings.NewReader(raw)}
			out, err := New().Aggressive(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			if len(body) >= len(raw) {
				t.Fatalf("no shrink: %d >= %d", len(body), len(raw))
			}
			for _, w := range tt.keep {
				if !strings.Contains(body, w) {
					t.Errorf("lost %q", w)
				}
			}
			for _, g := range tt.gone {
				if strings.Contains(body, g) {
					t.Errorf("kept boilerplate %q", g)
				}
			}
			if !strings.Contains(body, "…+") {
				t.Error("elision not marked")
			}
		})
	}
}

func TestDescribeNonZeroExitDeclines(t *testing.T) {
	in := format.Input{Argv: []string{"kubectl", "describe", "pod", "x"}, Stdout: strings.NewReader(kubectlDescribeFixture), ExitCode: 1}
	if _, err := New().Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("err = %v", err)
	}
}

func TestConfigViewElidesCredentialsNotTopology(t *testing.T) {
	raw := nativeFixture(t, "config-view-raw.txt")
	in := format.Input{Argv: []string{"kubectl", "config", "view", "--raw"}, Stdout: strings.NewReader(raw)}
	out, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, secret := range []string{"LS0tLS1CRUdJTiBFQyBQUklWQVRFIEtFWS", "fake-demo-token", "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS"} {
		if strings.Contains(body, secret) {
			t.Errorf("credential leaked: %s", secret)
		}
	}
	for _, want := range []string{"server: https://127.0.0.1:53026", "name: kind-demo", "client-key-data: …+", "token: …+"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestConfigViewMaskedByKubectlDeclines(t *testing.T) {
	raw := "clusters:\n- cluster:\n    certificate-authority-data: DATA+OMITTED\n    server: https://x\n"
	in := format.Input{Argv: []string{"kubectl", "config", "view"}, Stdout: strings.NewReader(raw)}
	if _, err := New().Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("err = %v", err)
	}
}

func TestGetAllRendersEachSection(t *testing.T) {
	raw := nativeFixture(t, "get-all-namespaces.txt")
	in := format.Input{Argv: []string{"kubectl", "get", "all", "-A"}, Stdout: strings.NewReader(raw)}
	out, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{"default pod/badimage 0/1 ImagePullBackOff", "pod/crasher 0/1 Error", "service/web", "deployment.apps/web", "job.batch/failjob Failed"} {
		if !strings.Contains(body, want) {
			t.Errorf("lost %q:\n%s", want, body)
		}
	}
	if len(body) >= len(raw) {
		t.Fatal("no shrink")
	}
}

func TestAggressiveDescribeEmpty(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"kubectl", "describe", "pod", "x"}, Stdout: strings.NewReader("")}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}
