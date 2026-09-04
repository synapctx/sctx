package agentsetup

// Small JSON helpers shared by the three hook files whose config is a plain
// JSON document rather than Claude/Gemini's shared settings.json map (hooks.go)
// or Codex's TOML (codexmcp.go): Cursor's hooks.json, Copilot's hook file, and
// Droid's hooks.json/settings.json.

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
)

// readJSONObject reads path as a JSON object. A missing file reads as an
// empty, writable object — never an error, since "not installed yet" is the
// normal fresh-machine case. An unparseable file IS an error: a config we
// cannot read is a config we must not silently overwrite.
func readJSONObject(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

// writeJSONObject serialises doc and writes it atomically, creating the
// parent directory if needed.
func writeJSONObject(path string, doc map[string]any) error {
	out, err := json.Marshal(doc, jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := writePrivateFile(path, append(out, '\n')); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func firstNonNil(v any, fallback any) any {
	if v == nil {
		return fallback
	}
	return v
}
