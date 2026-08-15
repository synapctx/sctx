package progkey

import "testing"

// TestKeyNeverLeaksArguments is the regression test for the defect this package was created
// to fix. Every case below is a real key found in SynapCTX's own collected telemetry, or
// the direct analogue of one, produced by a predicate that judged the TOKEN instead of the
// PROGRAM. A short lowercase word is indistinguishable from a subcommand, so hostnames and
// directory names were shipped to the platform under a comment promising they never were.
func TestKeyNeverLeaksArguments(t *testing.T) {
	for _, tc := range []struct{ program, next, want string }{
		// Observed in production telemetry.
		{"ssh", "vm", "ssh"},                    // a HOST
		{"find", "app", "find"},                 // a DIRECTORY
		{"find", "css", "find"},                 // a DIRECTORY
		{"find", "internal", "find"},            // a DIRECTORY
		{"ls", "migrations", "ls"},              // a DIRECTORY
		{"ls", "templates", "ls"},               // a DIRECTORY
		{"cp", "readme", "cp"},                  // a FILE
		{"rsync", "data", "rsync"},              // a PATH
		{"scp", "notes", "scp"},                 // a FILE
		{"sed", "s/a/b/", "sed"},                // a SCRIPT
		{"wc", "results", "wc"},                 // a FILE
		{"cat", "secrets", "cat"},               // a FILE
		{"head", "config", "head"},              // a FILE
		{"grep", "password", "grep"},            // a PATTERN — the worst possible leak
		{"rg", "apikey", "rg"},                  // a PATTERN
		{"curl", "example", "curl"},             // part of a URL
		{"tree", "src", "tree"},                 // a DIRECTORY
		{"du", "backups", "du"},                 // a DIRECTORY
		{"ssh", "prodbox", "ssh"},               // a HOST
		{"ssh", "customer", "ssh"},              // a HOST naming a CUSTOMER
		{"make", "deploy", "make"},              // a TARGET is an argument, not a subcommand
		{"for", "r", "for"},                     // a shell loop VARIABLE
		{"command", "go", "command"},            // shell keyword
		{"pytest", "tests", "pytest"},           // a PATH
		{"mypy", "app", "mypy"},                 // a PATH
		{"mongosh", "mydb", "mongosh"},          // a DATABASE NAME
		{"jq", "keys", "jq"},                    // a FILTER
		{"diff", "before", "diff"},              // a FILE
		{"ps", "aux", "ps"},                     // an argument
		{"gofmt", "app", "gofmt"},               // a PATH
		{"dig", "internal", "dig"},              // a DOMAIN
		{"psql", "production", "psql"},          // a DATABASE NAME
		{"terraform", "plan", "terraform plan"}, // genuinely a subcommand
	} {
		if got := Key(tc.program, tc.next); got != tc.want {
			t.Errorf("Key(%q, %q) = %q, want %q", tc.program, tc.next, got, tc.want)
		}
	}
}

func TestFromArgvGitGlobals(t *testing.T) {
	got := FromArgv([]string{"/usr/bin/git", "--no-pager", "-C", "/private/repo", "-c", "color.ui=false", "status", "--short"})
	if got != "git status" {
		t.Fatalf("FromArgv() = %q, want git status", got)
	}
}

// TestKeyKeepsRealSubcommands — the fix must not flatten everything. The subcommand is what
// makes the meter actionable: `terraform plan` is output-heavy where `terraform apply` is
// not, and a formatter is written for one and not the other.
func TestKeyKeepsRealSubcommands(t *testing.T) {
	for _, tc := range []struct{ program, next, want string }{
		{"git", "status", "git status"},
		{"git", "log", "git log"},
		{"go", "test", "go test"},
		{"go", "build", "go build"},
		{"cargo", "build", "cargo build"},
		{"cargo", "clippy", "cargo clippy"},
		{"docker", "ps", "docker ps"},
		{"kubectl", "get", "kubectl get"},
		{"gh", "pr", "gh pr"},
		{"npm", "install", "npm install"},
		{"terraform", "apply", "terraform apply"},
		{"aws", "s3", "aws s3"},
		{"helm", "upgrade", "helm upgrade"},
		{"systemctl", "status", "systemctl status"},
		{"pip", "install", "pip install"},
		{"golangci-lint", "run", "golangci-lint run"},
		{"pre-commit", "run", "pre-commit run"},
	} {
		if got := Key(tc.program, tc.next); got != tc.want {
			t.Errorf("Key(%q, %q) = %q, want %q", tc.program, tc.next, got, tc.want)
		}
	}
}

// TestKeyRejectsNonSubcommandShapes — the shape gate still matters for programs that DO
// take subcommands, or a flag or path becomes half the key.
func TestKeyRejectsNonSubcommandShapes(t *testing.T) {
	for _, tc := range []struct{ program, next, want string }{
		{"git", "-C", "git"},
		{"git", "--no-pager", "git"},
		{"go", "./...", "go"},
		{"docker", "--rm", "docker"},
		{"cargo", "+nightly", "cargo"},
		{"kubectl", "-n", "kubectl"},
		{"git", "Status", "git"},                       // uppercase: not a subcommand
		{"git", "averyveryverylongtokenindeed", "git"}, // over the length cap
		{"git", "", "git"},                             // no next token
		{"", "status", ""},                             // no program
	} {
		if got := Key(tc.program, tc.next); got != tc.want {
			t.Errorf("Key(%q, %q) = %q, want %q", tc.program, tc.next, got, tc.want)
		}
	}
}
