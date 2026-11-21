package agentsetup

// Auto-wrap for Trae.
//
// VERIFIED-FROM-THIRD-PARTY-SOURCE: a working third-party installer. Trae reads
// `~/.trae/hooks.json` (`~/.trae-cn/hooks.json` for the China build, which this
// package does not manage), a version-1 document whose `hooks.PreToolUse` is
// the same grouped `{"matcher", "hooks":[{"type":"command","command",
// "timeout"}]}` shape Claude Code uses, with the matcher `RunCommand`. That
// shape is why this file reuses findHookEntry/addHook from hooks.go. That
// installer refuses a root whose `version` is anything but 1, and so does this: a file
// in a format we have not seen is left alone.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/synapctx/sctx/internal/platform/binaries"
)

// TraeHookStatus is the auto-wrap state for Trae.
type TraeHookStatus struct {
	ConfigPath  string
	Installed   bool
	Stale       bool
	StaleReason string
	WiredTo     string
	Missing     string
}

func (s TraeHookStatus) Complete() bool { return s.Installed && !s.Stale }

func traeSpec(binary string) HookSpec {
	return HookSpec{
		Event:      "PreToolUse",
		Matcher:    "RunCommand",
		Subcommand: "trae",
		Command:    quoteBinaryForCommand(binary) + " hook trae",
		Timeout:    30,
	}
}

func traeConfigPath(home string) string { return filepath.Join(home, ".trae", "hooks.json") }

// traeVersionOK reports whether doc is a format we may edit.
func traeVersionOK(doc map[string]any) error {
	if v, ok := doc["version"]; ok {
		if n, isNum := v.(float64); !isNum || n != 1 {
			return fmt.Errorf("hooks.json version %v is not 1; left unchanged", v)
		}
	}
	return nil
}

// InspectTraeHooks reports whether Trae is wired to rewrite its commands.
func InspectTraeHooks(home, binary string) (TraeHookStatus, error) {
	st := TraeHookStatus{ConfigPath: traeConfigPath(home)}
	doc, err := readJSONObject(st.ConfigPath)
	if err != nil {
		return st, err
	}
	entry, found := findHookEntry(doc, traeSpec(binary))
	st.Installed = found
	if found {
		cmd, _ := entry["command"].(string)
		st.WiredTo = wiredBinary(cmd, "", " hook trae")
		if st.WiredTo != "" && !samePath(st.WiredTo, binary) {
			if _, err := os.Stat(st.WiredTo); err != nil {
				st.Stale = true
				st.Missing = st.WiredTo
				st.StaleReason = "the sctx it calls no longer exists"
			} else if reason, stale := StaleHookReason(st.WiredTo, binary, binaries.VersionOf(binary)); stale {
				st.Stale = true
				st.StaleReason = reason
			}
		}
	}
	return st, nil
}

// InstallTraeHooks adds the sctx PreToolUse/RunCommand entry to hooks.json,
// preserving everything else, and rewires a stale entry in place.
func InstallTraeHooks(home, binary string) ([]string, error) {
	path := traeConfigPath(home)
	doc, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	if err := traeVersionOK(doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	spec := traeSpec(binary)
	if entry, found := findHookEntry(doc, spec); found {
		st, err := InspectTraeHooks(home, binary)
		if err != nil || !st.Stale {
			return nil, err
		}
		entry["command"] = spec.Command
		if err := writeJSONObject(path, doc); err != nil {
			return nil, err
		}
		return []string{rewireMessage("Trae", st.WiredTo, binary)}, nil
	}
	doc["version"] = firstNonNil(doc["version"], 1)
	addHook(doc, spec)
	if err := writeJSONObject(path, doc); err != nil {
		return nil, err
	}
	return []string{fmt.Sprintf("installed the Trae auto-wrap hook (%s)", path)}, nil
}
