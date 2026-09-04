package projectfilter

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

type trustFile struct {
	Version  int               `json:"version"`
	Projects map[string]string `json:"projects"`
}

func DefaultTrustPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "sctx", "filter-trust.json"), nil
}

func (l Loaded) Trusted(trustPath string) (bool, error) {
	store, err := loadTrust(trustPath)
	if err != nil {
		return false, err
	}
	return store.Projects[filepath.Clean(l.Root)] == l.Digest, nil
}

// Trust records approval for this exact project path and filter-file digest.
// Any content change or checkout move invalidates it.
func (l Loaded) Trust(trustPath string) error {
	store, err := loadTrust(trustPath)
	if err != nil {
		return err
	}
	store.Projects[filepath.Clean(l.Root)] = l.Digest
	return saveTrust(trustPath, store)
}

func loadTrust(path string) (trustFile, error) {
	empty := trustFile{Version: 1, Projects: map[string]string{}}
	if err := validateTrustDirectory(filepath.Dir(path)); err != nil {
		return trustFile{}, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return empty, nil
	}
	if err != nil {
		return trustFile{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return trustFile{}, errors.New("project filter trust file must be a regular non-symlink file")
	}
	// Windows has no POSIX mode bits; os.FileMode there is synthesized from
	// ACLs and does not reflect real group/world writability, so this check
	// only applies on POSIX platforms.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return trustFile{}, errors.New("project filter trust file must not be group/world writable")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return trustFile{}, err
	}
	var store trustFile
	dec := jsontext.NewDecoder(bytes.NewReader(raw))
	if err := json.UnmarshalDecode(dec, &store, json.RejectUnknownMembers(true)); err != nil {
		return trustFile{}, fmt.Errorf("decoding project filter trust: %w", err)
	}
	if json.UnmarshalDecode(dec, &struct{}{}) != io.EOF || store.Version != 1 || store.Projects == nil {
		return trustFile{}, errors.New("invalid project filter trust file")
	}
	return store, nil
}

func saveTrust(path string, store trustFile) error {
	dir := filepath.Dir(path)
	if err := validateTrustDirectory(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(store, jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(dir, ".filter-trust-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	remove := true
	defer func() {
		_ = tmp.Close()
		if remove {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	remove = false
	return nil
}

func validateTrustDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("project filter trust directory must be a non-symlink directory")
	}
	// Windows ACLs are not visible through os.FileMode, so this check only
	// applies on POSIX platforms; see loadTrust above.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return errors.New("project filter trust directory must not be group/world writable")
	}
	return nil
}

// LoadTrustedFrom discovers a project, validates its fixtures, and returns it
// only if the exact digest has been approved in the user-owned trust store.
func LoadTrustedFrom(start, trustPath string) (Loaded, bool, error) {
	loaded, found, err := LoadFrom(start)
	if err != nil || !found {
		return Loaded{}, false, err
	}
	trusted, err := loaded.Trusted(trustPath)
	if err != nil || !trusted {
		return Loaded{}, false, err
	}
	return loaded, true, nil
}
