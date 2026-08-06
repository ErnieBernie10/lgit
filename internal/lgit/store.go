package lgit

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Project struct {
	ID          string `json:"id"`
	GitDir      string `json:"gitDir"`
	Slug        string `json:"slug"`
	Environment string `json:"environment"`
}
type Registry struct {
	Remote   string             `json:"remote,omitempty"`
	Projects map[string]Project `json:"projects"`
}

func DataDir() (string, error) {
	if v := os.Getenv("LGIT_DATA_DIR"); v != "" {
		return filepath.Abs(v)
	}
	b, e := os.UserConfigDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(b, "lgit"), nil
}
func LoadRegistry(path string) (Registry, error) {
	b, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		return Registry{Projects: map[string]Project{}}, nil
	}
	if e != nil {
		return Registry{}, e
	}
	var r Registry
	if e = json.Unmarshal(b, &r); e != nil {
		return Registry{}, e
	}
	if r.Projects == nil {
		r.Projects = map[string]Project{}
	}
	return r, nil
}
func SaveRegistry(path string, r Registry) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, path)
}
func newID() (string, error) {
	b := make([]byte, 8)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
