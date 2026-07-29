// Package corpus loads captured raw proto payloads written by Golbat's
// raw_capture worker: <dir>/<METHOD>/<unixMilli>_<sizeBytes>.bin.
// The corpus grows incrementally; whatever exists is what gets benchmarked.
package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Payload struct {
	Method string
	Path   string
	Data   []byte
}

func Load(dir string) (map[string][]Payload, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("corpus dir: %w", err)
	}
	out := make(map[string][]Payload)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		method := e.Name()
		files, err := os.ReadDir(filepath.Join(dir, method))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".bin") {
				continue
			}
			path := filepath.Join(dir, method, f.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			out[method] = append(out[method], Payload{Method: method, Path: path, Data: data})
		}
	}
	return out, nil
}
