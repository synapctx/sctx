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

func TestAggressiveDescribe(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"kubectl", "describe", "pod", "web-7d8f9c6b5d-abcde"}, Stdout: strings.NewReader(kubectlDescribeFixture)}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)

	for _, want := range []string{
		"Name:             web-7d8f9c6b5d-abcde",
		"Namespace:        default",
		"Status:           Running",
		"Containers:",
		"Conditions:",
		"Events:",
		"Normal  Scheduled  5m    default-scheduler  Successfully assigned default/web-7d8f9c6b5d-abcde to node-1",
		"Normal  Started    5m    kubelet            Started container web",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q, got: %q", want, body)
		}
	}
	if !strings.Contains(body, "…+") {
		t.Errorf("body missing elision marker, got: %q", body)
	}
	if strings.Contains(body, "Container ID:  containerd://abc123") {
		t.Errorf("body still contains elided container detail: %q", body)
	}
	if len(out.Body) >= len(kubectlDescribeFixture) {
		t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(kubectlDescribeFixture))
	}
}

func TestAggressiveDescribeEmpty(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"kubectl", "describe", "pod", "x"}, Stdout: strings.NewReader("")}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}
