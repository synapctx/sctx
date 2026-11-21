package openssl

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func run(argv []string, raw string, exit int) (format.Rendered, error) {
	return New().Aggressive(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader(raw), ExitCode: exit})
}

func TestRenders(t *testing.T) {
	cases := []struct {
		file string
		argv []string
		keep []string
		drop []string
		note string
	}{
		{"x509.txt", []string{"openssl", "x509", "-in", "go.pem", "-text", "-noout"},
			[]string{
				"Issuer: C=US, O=Google Trust Services, CN=WR3",
				"Not Before: Nov 11 22:57:52 2026 GMT",
				"Not After : Dec 26 23:47:23 2026 GMT",
				"Subject: CN=go.dev",
				"Public Key Algorithm: rsaEncryption",
				"Public-Key: (2048 bit)",
				"Exponent: 65537 (0x10001)",
				"DNS:go.dev",
				"CA:FALSE",
				"C2:8E:36:DB:75:F4:C7:6E:84:38:9C:50:D2:C6:01:7B:2F:6B:D7:71", // short key id stays
				"Modulus:\n                    …+16 hex rows (256 bytes)",
				"Signature Value:\n        …+11 hex rows (256 bytes)",
				"Log ID    : 25:B7:EF",
			},
			[]string{"a0:34:3d:9d", "58:fa:ed:28"}, ""},
		{"sclient13.txt", []string{"openssl", "s_client", "-connect", "go.dev:443", "-servername", "go.dev"},
			[]string{
				"Certificate chain", "0 s:CN=go.dev", "a:PKEY: RSA, 2048 (bit); sigalg: sha256WithRSAEncryption",
				"…+29 lines (CERTIFICATE)", "subject=CN=go.dev", "Verification: OK", "Verify return code: 0 (ok)", "Protocol: TLSv1.3",
			},
			[]string{"MIIE9TCC", "-----BEGIN"}, ""},
		{"sclient12.txt", []string{"openssl", "s_client", "-connect", "go.dev:443", "-tls1_2"},
			[]string{"Verify return code: 0 (ok)", "Protocol  : TLSv1.2", "…+"}, []string{"-----BEGIN"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw := fixture(t, tc.file)
			out, err := run(tc.argv, raw, 0)
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			if len(body) >= len(raw) {
				t.Errorf("not smaller %d >= %d", len(body), len(raw))
			}
			if !out.Elided {
				t.Error("Elided not set")
			}
			for _, k := range tc.keep {
				if !strings.Contains(body, k) {
					t.Errorf("lost %q:\n%s", k, body)
				}
			}
			for _, d := range tc.drop {
				if strings.Contains(body, d) {
					t.Errorf("kept %q", d)
				}
			}
		})
	}
}

func TestDeclines(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		file string
		exit int
	}{
		{"x509 without -text", []string{"openssl", "x509", "-in", "f", "-noout", "-subject", "-dates"}, "x509-small.txt", 0},
		{"x509 -pubkey", []string{"openssl", "x509", "-in", "f", "-text", "-pubkey"}, "x509.txt", 0},
		{"x509 -modulus", []string{"openssl", "x509", "-in", "f", "-text", "-modulus"}, "x509.txt", 0},
		{"missing file", []string{"openssl", "x509", "-in", "nonexist.pem", "-text", "-noout"}, "x509-fail.txt", 1},
		{"s_client -showcerts", []string{"openssl", "s_client", "-connect", "h:443", "-showcerts"}, "showcerts.txt", 0},
		{"s_client resolve failure", []string{"openssl", "s_client", "-connect", "nonexistent.invalid:443"}, "sclient-fail.txt", 1},
		{"other subcommand", []string{"openssl", "rand", "-hex", "8"}, "x509.txt", 0},
		{"version", []string{"openssl", "version"}, "x509-small.txt", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := run(tc.argv, fixture(t, tc.file), tc.exit); !errors.Is(err, format.ErrTierInapplicable) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestNonZeroExitKeepsErrorLines(t *testing.T) {
	raw := fixture(t, "sclient13.txt") + "Verify return code: 20 (unable to get local issuer certificate)\n"
	out, err := run([]string{"openssl", "s_client", "-connect", "h:443"}, raw, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Body), "Verify return code: 20 (unable to get local issuer certificate)") {
		t.Errorf("lost verify failure:\n%s", out.Body)
	}
}

func TestUnterminatedPEMIsKept(t *testing.T) {
	got := dropPEM([]string{"a", "-----BEGIN CERTIFICATE-----", "AAAA", "b"})
	if len(got) != 4 {
		t.Errorf("got %v", got)
	}
}
