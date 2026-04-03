package task

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Store interface {
	Load() (map[string]*Record, error)
	Save(records map[string]*Record) error
}

type FileStore struct {
	path string
}

func NewFileStore(dataDir string) *FileStore {
	return &FileStore{path: filepath.Join(dataDir, "tasks.json")}
}

func (s *FileStore) Load() (map[string]*Record, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]*Record), nil
		}
		return nil, err
	}

	loaded := make(map[string]*Record)
	if err := json.Unmarshal(data, &loaded); err != nil {
		return nil, err
	}

	return loaded, nil
}

func (s *FileStore) Save(records map[string]*Record) error {
	cloned := make(map[string]*Record, len(records))
	for id, record := range records {
		cloned[id] = cloneRecord(record)
	}

	data, err := json.MarshalIndent(cloned, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}

	return os.WriteFile(s.path, data, 0644)
}
