package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/andocodes/cassie/internal/domain/catalog"
)

const DefaultAPIURL = "https://infisical.localhost/api"

type Infisical struct {
	Binary string
	APIURL string
	Env    map[string]string
}

func (i Infisical) Load(ctx context.Context, binding catalog.SecretBinding) (map[string]string, error) {
	if !binding.Enabled() {
		return nil, nil
	}
	binary := i.Binary
	if binary == "" {
		binary = "infisical"
	}
	apiURL := i.APIURL
	if apiURL == "" {
		apiURL = DefaultAPIURL
	}
	args := []string{"export", "--format=json", "--silent", "--projectId=" + binding.Project}
	if binding.Environment != "" {
		args = append(args, "--env="+binding.Environment)
	}
	if binding.Path != "" {
		args = append(args, "--path="+binding.Path)
	}
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = append(os.Environ(), "INFISICAL_API_URL="+apiURL)
	for key, value := range i.Env {
		command.Env = append(command.Env, key+"="+value)
	}
	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("load Infisical secrets: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("load Infisical secrets: %w", err)
	}
	var values map[string]any
	if err := json.Unmarshal(output, &values); err != nil {
		return nil, fmt.Errorf("decode Infisical export: %w", err)
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = fmt.Sprint(value)
	}
	return result, nil
}
