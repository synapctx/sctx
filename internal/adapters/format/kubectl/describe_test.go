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

func TestDescribeWithoutProvableRepetitionStaysVerbatim(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"kubectl", "describe", "pod", "web-7d8f9c6b5d-abcde"}, Stdout: strings.NewReader(kubectlDescribeFixture)}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("Aggressive() error = %v, want inapplicable so every resource-specific field remains native", err)
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
	for _, want := range []string{"Image:         nginx:1.25", "Container ID:  containerd://abc123", "Repeated diagnostic ×3"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestAggressiveDescribeEmpty(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"kubectl", "describe", "pod", "x"}, Stdout: strings.NewReader("")}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}
