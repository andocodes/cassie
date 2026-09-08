package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func endpointPath(stateDir string) string {
	return filepath.Join(stateDir, "daemon.endpoint.json")
}

func writeEndpoint(stateDir string, value endpoint) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode daemon endpoint: %w", err)
	}
	temporary, err := os.CreateTemp(stateDir, "daemon.endpoint-*")
	if err != nil {
		return fmt.Errorf("create daemon endpoint: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect daemon endpoint: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write daemon endpoint: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close daemon endpoint: %w", err)
	}
	if err := os.Rename(temporaryPath, endpointPath(stateDir)); err != nil {
		return fmt.Errorf("publish daemon endpoint: %w", err)
	}
	return nil
}

func readEndpoint(stateDir string) (endpoint, error) {
	value, err := os.ReadFile(endpointPath(stateDir))
	if err != nil {
		return endpoint{}, fmt.Errorf("read daemon endpoint: %w", err)
	}
	var result endpoint
	if err := json.Unmarshal(value, &result); err != nil {
		return endpoint{}, fmt.Errorf("decode daemon endpoint: %w", err)
	}
	if result.Version != protocolVersion || result.Network == "" || result.Address == "" || result.Token == "" {
		return endpoint{}, fmt.Errorf("invalid daemon endpoint")
	}
	return result, nil
}
