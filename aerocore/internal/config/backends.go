package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/swaroop/aero/aerocore/internal/placement"
	"github.com/swaroop/aero/aerocore/pkg/api"
)

type backendFile struct {
	Backends []placement.Backend `json:"backends"`
}

func LoadBackends(path string) ([]placement.Backend, error) {
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read backends file: %w", err)
	}

	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, errors.New("backends file is empty")
	}

	var backends []placement.Backend

	if data[0] == '[' {
		if err := json.Unmarshal(data, &backends); err != nil {
			return nil, fmt.Errorf("decode backends array: %w", err)
		}
	} else {
		var file backendFile
		if err := json.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("decode backends object: %w", err)
		}
		backends = file.Backends
	}

	if err := validateBackends(backends); err != nil {
		return nil, err
	}

	return backends, nil
}

func validateBackends(backends []placement.Backend) error {
	seen := make(map[string]struct{}, len(backends))

	for _, b := range backends {
		if err := api.ValidateBackend(b); err != nil {
			return fmt.Errorf("backend %q invalid: %w", b.ID, err)
		}

		if _, ok := seen[b.ID]; ok {
			return fmt.Errorf("duplicate backend id %q", b.ID)
		}
		seen[b.ID] = struct{}{}
	}

	return nil
}
