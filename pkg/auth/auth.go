// Package auth stores one API key per provider in ~/.kaku/auth.json so a user
// can log in once instead of exporting an environment variable each session.
// The file is written 0600 and keys leave it only through Get.
package auth

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Store is a credential store over a JSON file mapping provider name to key.
type Store struct {
	path string
}

// New opens the store at the default path (~/.kaku/auth.json).
func New() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(home, ".kaku", "auth.json")}, nil
}

// NewAt opens a store at an explicit path. It exists mainly for tests.
func NewAt(path string) *Store {
	return &Store{path: path}
}

func (s *Store) load() (map[string]string, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if len(data) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Store) save(m map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.path, data, 0o600)
}

// Get returns the stored key for a provider and whether a non-empty one exists.
func (s *Store) Get(provider string) (string, bool) {
	m, err := s.load()
	if err != nil {
		return "", false
	}
	k, ok := m[provider]
	return k, ok && k != ""
}

// Set stores a key for a provider, creating the file 0600 if it is missing.
func (s *Store) Set(provider, key string) error {
	if provider == "" {
		return errors.New("auth: provider is required")
	}
	m, err := s.load()
	if err != nil {
		return err
	}
	m[provider] = key
	return s.save(m)
}

// Delete removes a provider's key. Removing an absent key is a no-op, not an
// error, so logout is idempotent.
func (s *Store) Delete(provider string) error {
	m, err := s.load()
	if err != nil {
		return err
	}
	if _, ok := m[provider]; !ok {
		return nil
	}
	delete(m, provider)
	return s.save(m)
}

// List returns the provider names that have a stored key, sorted. Values are
// never returned so a listing cannot leak a key.
func (s *Store) List() []string {
	m, err := s.load()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
