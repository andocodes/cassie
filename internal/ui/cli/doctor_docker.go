package cli

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
)

type dockerInfo struct {
	Name            string `json:"Name"`
	OperatingSystem string `json:"OperatingSystem"`
	ServerVersion   string `json:"ServerVersion"`
}

func dockerDetail(ctx context.Context, output []byte) string {
	fallback := firstLine(string(output))
	var info dockerInfo
	if err := json.Unmarshal(output, &info); err != nil || info.ServerVersion == "" {
		return fallback
	}
	parts := make([]string, 0, 3)
	if provider := dockerProvider(info); provider != "" {
		parts = append(parts, provider)
	}
	parts = append(parts, "Engine "+info.ServerVersion)
	if contextOutput, err := exec.CommandContext(ctx, "docker", "context", "show").Output(); err == nil {
		if name := strings.TrimSpace(string(contextOutput)); name != "" {
			parts = append(parts, "context "+name)
		}
	}
	return strings.Join(parts, " · ")
}

func dockerProvider(info dockerInfo) string {
	operatingSystem := strings.TrimSpace(info.OperatingSystem)
	lower := strings.ToLower(operatingSystem)
	switch {
	case strings.Contains(lower, "orbstack"):
		return "OrbStack"
	case strings.Contains(lower, "docker desktop"):
		return "Docker Desktop"
	case strings.Contains(lower, "colima"):
		return "Colima"
	case operatingSystem != "" && !strings.HasPrefix(lower, "docker engine"):
		return operatingSystem
	case strings.TrimSpace(info.Name) != "":
		return strings.TrimSpace(info.Name)
	default:
		return ""
	}
}
