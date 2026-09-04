package sshargv

import (
	"strings"
	"testing"
)

func TestRemoteCommand(t *testing.T) {
	tests := []struct {
		argv []string
		want string
		ok   bool
	}{
		{[]string{"ssh", "host", "go test ./..."}, "go test ./...", true},
		{[]string{"ssh", "-p", "2222", "-oBatchMode=yes", "host", "docker", "ps"}, "docker ps", true},
		{[]string{"ssh", "-T", "host", "go", "test"}, "go test", true},
		{[]string{"ssh", "host"}, "", false},
		{[]string{"ssh", "-N", "host", "go test"}, "", false},
		{[]string{"ssh", "-t", "host", "go test"}, "", false},
		{[]string{"ssh", "host", "sh -c 'go test'"}, "", false},
		// A pipeline into a narrowing tail (head/tail/cat/less/more) delegates
		// to the HEAD program's formatter; anything else piped stays declined.
		{[]string{"ssh", "host", "go test | head"}, "go test", true},
		{[]string{"ssh", "host", "go test | grep FAIL"}, "", false},
		{[]string{"ssh", "host", "ssh other go test"}, "", false},
	}
	for _, tt := range tests {
		got, ok := RemoteCommand(tt.argv)
		if ok != tt.ok || strings.Join(got, " ") != tt.want {
			t.Errorf("RemoteCommand(%v) = %q, %v; want %q, %v", tt.argv, got, ok, tt.want, tt.ok)
		}
	}
}
