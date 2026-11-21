package mongosh

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const banner = `Current Mongosh Log ID:	65f1a2b3c4d5e6f7a8b9c0d1
Connecting to:		mongodb://127.0.0.1:27017/?directConnection=true&appName=mongosh+2.1.1
Using MongoDB:		7.0.5
Using Mongosh:		2.1.1

For mongosh info see: https://www.mongodb.com/docs/mongodb-shell/

To help improve our products, anonymous usage data is collected and sent to MongoDB periodically. To disable this reporting, run and save disableTelemetry() in the shell, or add the --norc flag when starting the shell.

------
   The server generated these startup warnings when booting
   2024-01-01T00:00:00.000+00:00: Access control is not enabled for the database
------

`

// jsonRelaxedFixture is realistic `mongosh --json=relaxed` output: a banner
// (since --quiet wasn't also passed) followed by a valid JSON array of
// documents.
const jsonRelaxedFixture = banner + `[
  { "_id": "64f1a2b3c4d5e6f7a8b9c0d0", "name": "user-0", "age": 30, "created": "2023-01-01T00:00:00.000Z" },
  { "_id": "64f1a2b3c4d5e6f7a8b9c0d1", "name": "user-1", "age": 31, "created": "2023-01-02T00:00:00.000Z" },
  { "_id": "64f1a2b3c4d5e6f7a8b9c0d2", "name": "user-2", "age": 32, "created": "2023-01-03T00:00:00.000Z" },
  { "_id": "64f1a2b3c4d5e6f7a8b9c0d3", "name": "user-3", "age": 33, "created": "2023-01-04T00:00:00.000Z" },
  { "_id": "64f1a2b3c4d5e6f7a8b9c0d4", "name": "user-4", "age": 34, "created": "2023-01-05T00:00:00.000Z" },
  { "_id": "64f1a2b3c4d5e6f7a8b9c0d5", "name": "user-5", "age": 35, "created": "2023-01-06T00:00:00.000Z" },
  { "_id": "64f1a2b3c4d5e6f7a8b9c0d6", "name": "user-6", "age": 36, "created": "2023-01-07T00:00:00.000Z" },
  { "_id": "64f1a2b3c4d5e6f7a8b9c0d7", "name": "user-7", "age": 37, "created": "2023-01-08T00:00:00.000Z" },
  { "_id": "64f1a2b3c4d5e6f7a8b9c0d8", "name": "user-8", "age": 38, "created": "2023-01-09T00:00:00.000Z" },
  { "_id": "64f1a2b3c4d5e6f7a8b9c0d9", "name": "user-9", "age": 39, "created": "2023-01-10T00:00:00.000Z" },
  { "_id": "64f1a2b3c4d5e6f7a8b9c0da", "name": "user-10", "age": 40, "created": "2023-01-11T00:00:00.000Z" },
  { "_id": "64f1a2b3c4d5e6f7a8b9c0db", "name": "user-11", "age": 41, "created": "2023-01-12T00:00:00.000Z" }
]
`

// shellObjectFixture is realistic default (non --json) mongosh output: a
// banner followed by the shell-object printer's array of documents, using
// ObjectId()/ISODate() wrappers that make the payload NOT valid JSON.
const shellObjectFixture = banner + `[
  {
    _id: ObjectId('64f1a2b3c4d5e6f7a8b9c0d0'),
    name: 'user-0',
    age: 30,
    created: ISODate('2023-01-01T00:00:00.000Z')
  },
  {
    _id: ObjectId('64f1a2b3c4d5e6f7a8b9c0d1'),
    name: 'user-1',
    age: 31,
    created: ISODate('2023-01-02T00:00:00.000Z')
  },
  {
    _id: ObjectId('64f1a2b3c4d5e6f7a8b9c0d2'),
    name: 'user-2',
    age: 32,
    created: ISODate('2023-01-03T00:00:00.000Z')
  },
  {
    _id: ObjectId('64f1a2b3c4d5e6f7a8b9c0d3'),
    name: 'user-3',
    age: 33,
    created: ISODate('2023-01-04T00:00:00.000Z')
  },
  {
    _id: ObjectId('64f1a2b3c4d5e6f7a8b9c0d4'),
    name: 'user-4',
    age: 34,
    created: ISODate('2023-01-05T00:00:00.000Z')
  }
]
`

func TestAggressive(t *testing.T) {
	f := New()

	t.Run("json=relaxed array: banner stripped, jsoncompact array cap applied", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"mongosh", "--quiet=false", "--json=relaxed", "--eval", "db.users.find().toArray()"},
			Stdout: strings.NewReader(jsonRelaxedFixture),
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if strings.Contains(body, "Current Mongosh Log ID") || strings.Contains(body, "Connecting to:") {
			t.Errorf("banner not stripped from JSON body: %q", body)
		}
		if !strings.Contains(body, "more items") && !strings.Contains(body, "…") && !strings.HasPrefix(body, "T[12]{") {
			t.Errorf("expected an elision marker or a lossless table from jsoncompact, got: %q", body)
		}
		if len(out.Body) >= len(jsonRelaxedFixture) {
			t.Errorf("expected compaction, got body len %d >= input len %d", len(out.Body), len(jsonRelaxedFixture))
		}
	})

	t.Run("default shell-object array: banner stripped, every document kept under the cap", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"mongosh", "--eval", "db.users.find().toArray()"},
			Stdout: strings.NewReader(shellObjectFixture),
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if strings.Contains(body, "Current Mongosh Log ID") || strings.Contains(body, "Connecting to:") {
			t.Errorf("banner not stripped from shell-object body: %q", body)
		}
		if strings.Contains(body, "…+") || out.Elided {
			t.Errorf("5 documents are under the cap and must all be kept: %q", body)
		}
		if strings.Count(body, "ObjectId(") != 5 {
			t.Errorf("expected all 5 documents, got body: %q", body)
		}
		if len(out.Body) >= len(shellObjectFixture) {
			t.Errorf("expected compaction, got body len %d >= input len %d", len(out.Body), len(shellObjectFixture))
		}
	})

	t.Run("single scalar result: nothing to compress, tier inapplicable", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"mongosh", "--quiet", "--eval", "db.users.countDocuments()"},
			Stdout: strings.NewReader("42\n"),
		}
		_, err := f.Aggressive(context.Background(), in)
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("non-zero exit: aggressive degrades so error text is never structurally mangled", func(t *testing.T) {
		in := format.Input{
			Argv:     []string{"mongosh", "--eval", "db.users.insertOne({bad: 1})"},
			Stdout:   strings.NewReader(""),
			Stderr:   strings.NewReader("MongoServerError: E11000 duplicate key error collection: test.users index: _id_\n"),
			ExitCode: 1,
		}
		_, err := f.Aggressive(context.Background(), in)
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable on non-zero exit", err)
		}
	})

	t.Run("non-mongosh blob: no banner, no JSON, no document array, tier inapplicable", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"mongosh"},
			Stdout: strings.NewReader("hello\nworld\n"),
		}
		_, err := f.Aggressive(context.Background(), in)
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable", err)
		}
	})
}

func TestRelaxed(t *testing.T) {
	f := New()

	t.Run("banner stripped, error text preserved on non-zero exit", func(t *testing.T) {
		in := format.Input{
			Argv:     []string{"mongosh", "--eval", "db.users.insertOne({bad: 1})"},
			Stdout:   strings.NewReader(banner),
			Stderr:   strings.NewReader("Error: MongoServerError: E11000 duplicate key error collection: test.users index: _id_\nMongoServerError: E11000 duplicate key error collection: test.users index: _id_\n"),
			ExitCode: 1,
		}
		out, err := f.Relaxed(context.Background(), in)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		if !out.FoldStderr {
			t.Errorf("expected FoldStderr=true (stderr folded into Body)")
		}
		if !strings.Contains(string(out.Body), "MongoServerError") {
			t.Errorf("error text not preserved in relaxed body: %q", out.Body)
		}
		if strings.Contains(string(out.Body), "Connecting to:") {
			t.Errorf("banner leaked into relaxed body: %q", out.Body)
		}
	})

	t.Run("quiet output with repeated indented lines: dedupe applies, no banner present", func(t *testing.T) {
		quietOut := "  inserted\n  inserted\n  inserted\n  inserted\n  done\n"
		in := format.Input{
			Argv:   []string{"mongosh", "--quiet", "--eval", "for (let i=0;i<4;i++){print('inserted')}; print('done')"},
			Stdout: strings.NewReader(quietOut),
		}
		out, err := f.Relaxed(context.Background(), in)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		if !strings.Contains(string(out.Body), "inserted ×4") {
			t.Errorf("expected deduped '×4' marker, got: %q", out.Body)
		}
		if strings.Contains(string(out.Body), "  inserted") {
			t.Errorf("leading indentation was not collapsed: %q", out.Body)
		}
	})

	t.Run("no banner, no filterable structure: tier inapplicable", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"mongosh"},
			Stdout: strings.NewReader("hello\nworld\n"),
		}
		_, err := f.Relaxed(context.Background(), in)
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("Relaxed() error = %v, want ErrTierInapplicable", err)
		}
	})
}

func TestDescriptor(t *testing.T) {
	f := New()
	got := f.Descriptor()
	if got.Command != "mongosh" {
		t.Errorf("Descriptor().Command = %q, want %q", got.Command, "mongosh")
	}
	if len(got.Subcommands) != 0 {
		t.Errorf("Descriptor().Subcommands = %v, want none (mongosh's action is in --eval)", got.Subcommands)
	}
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestRealMongo8Output(t *testing.T) {
	for _, tc := range []struct {
		file    string
		wantAll []string
		marker  string
		elided  bool
	}{
		{"mongo8_find240.stdout", []string{"_id: 1,", "_id: 150,", "_id: 191,", "_id: 240,"}, "…+40 more documents", true},
		{"mongo8_find240.json", []string{`"_id":1,`, `"_id":150`, `"_id":191`, `"_id":240`}, "…+40 more documents", true},
		{"mongo8_find20.stdout", []string{"_id: 1,", "_id: 20,", "ISODate('2026-01-01T00:00:20.000Z')"}, "", false},
		{"mongo8_findone.stdout", []string{"_id: 7,", "name: 'user7'"}, "", false},
	} {
		t.Run(tc.file, func(t *testing.T) {
			raw := fixture(t, tc.file)
			out, err := New().Aggressive(context.Background(), format.Input{Stdout: strings.NewReader(raw)})
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			if len(body) >= len(raw) {
				t.Fatalf("not smaller: %d >= %d", len(body), len(raw))
			}
			for _, w := range tc.wantAll {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q", w)
				}
			}
			if tc.marker != "" && !strings.Contains(body, tc.marker) {
				t.Errorf("missing marker %q", tc.marker)
			}
			if tc.marker == "" && strings.Contains(body, "…+") {
				t.Error("unexpected elision under the cap")
			}
			if out.Elided != tc.elided {
				t.Errorf("Elided = %v, want %v", out.Elided, tc.elided)
			}
			if tc.marker != "" && strings.Contains(body, "_id: 151,") {
				t.Error("middle document retained")
			}
		})
	}
}

func TestFailedRunsKeepErrorText(t *testing.T) {
	for _, name := range []string{"mongo8_refused.stderr", "mongo8_authfail.stderr", "mongo8_syntax.stderr"} {
		t.Run(name, func(t *testing.T) {
			raw := fixture(t, name)
			in := func() format.Input {
				return format.Input{Stdout: strings.NewReader(""), Stderr: strings.NewReader(raw), ExitCode: 1}
			}
			if _, err := New().Aggressive(context.Background(), in()); !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("aggressive must decline on failure, got %v", err)
			}
			out, err := New().Relaxed(context.Background(), in())
			if err == nil && !strings.Contains(string(out.Body), strings.SplitN(strings.TrimSpace(raw), "\n", 2)[0]) {
				t.Errorf("error line lost: %q", out.Body)
			}
		})
	}
}
