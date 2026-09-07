package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andocodes/cassie/internal/adapters/git"
	"github.com/andocodes/cassie/internal/domain/catalog"
	"github.com/andocodes/cassie/internal/ports"
	"gopkg.in/yaml.v3"
)

const repoConfigName = ".cassie.yaml"

type Resolved struct {
	Application catalog.Application
	Sources     []string
	Trust       *Trust
}

type Trust struct {
	Path   string
	Digest string
}

type Resolver struct {
	UserPath string
	Git      ports.RepositoryInspector
}

func DefaultUserPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "cassie", "config.yaml"), nil
}

func (r Resolver) Resolve(ctx context.Context, dir, requestedName string) (Resolved, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve working directory: %w", err)
	}

	repository, repoErr := r.Git.Inspect(ctx, dir)
	if repoErr != nil && !errors.Is(repoErr, git.ErrNotRepository) {
		return Resolved{}, fmt.Errorf("inspect repository: %w", repoErr)
	}
	if errors.Is(repoErr, git.ErrNotRepository) {
		repository = ports.Repository{Root: dir}
	}

	baseName := sanitizeName(filepath.Base(repository.Root))
	effective := map[string]any{
		"name":   baseName,
		"domain": baseName,
		"grace":  "10s",
	}
	var sources []string

	user, userExists, err := readMap(r.UserPath)
	if err != nil {
		return Resolved{}, err
	}
	if userExists {
		if defaults := childMap(user, "defaults"); defaults != nil {
			effective = merge(effective, defaults)
			sources = append(sources, r.UserPath+"#defaults")
		}
	}

	workspacePath := findWorkspaceConfig(repository.Root)
	if workspacePath != "" {
		workspace, _, loadErr := readMap(workspacePath)
		if loadErr != nil {
			return Resolved{}, loadErr
		}
		if defaults := childMap(workspace, "defaults"); defaults != nil {
			effective = merge(effective, defaults)
			sources = append(sources, workspacePath+"#defaults")
		}
	}

	repoPath := findRepoConfig(dir, repository.Root)
	var repoConfig map[string]any
	if repoPath != "" {
		repoConfig, _, err = readMap(repoPath)
		if err != nil {
			return Resolved{}, err
		}
	}

	lookupName := requestedName
	if lookupName == "" && repoConfig != nil {
		lookupName, _ = repoConfig["name"].(string)
	}

	if workspacePath != "" {
		workspace, _, _ := readMap(workspacePath)
		if name, app := matchApp(childMap(workspace, "apps"), lookupName, repository, dir); app != nil {
			effective = merge(effective, app)
			if stringValue(effective["name"]) == "" {
				effective["name"] = name
			}
			sources = append(sources, workspacePath+"#apps."+name)
		}
	}

	if userExists {
		if name, app := matchApp(childMap(user, "apps"), lookupName, repository, dir); app != nil {
			effective = merge(effective, app)
			if _, set := effective["name"]; !set || effective["name"] == "" {
				effective["name"] = name
			}
			if strings.TrimSpace(stringValue(effective["name"])) == baseName && requestedName == "" {
				effective["name"] = name
			}
			sources = append(sources, r.UserPath+"#apps."+name)
		}
	}

	if repoConfig != nil {
		effective = merge(effective, applicationFields(repoConfig))
		sources = append(sources, repoPath)
	}

	root := applicationRoot(repository.Root, dir, effective)
	if repoPath != "" {
		root = filepath.Dir(repoPath)
	}
	delete(effective, "match")
	encoded, err := yaml.Marshal(effective)
	if err != nil {
		return Resolved{}, fmt.Errorf("encode effective configuration: %w", err)
	}
	var app catalog.Application
	decoder := yaml.NewDecoder(strings.NewReader(string(encoded)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&app); err != nil {
		return Resolved{}, fmt.Errorf("decode effective configuration: %w", err)
	}
	app.Root = root
	if err := app.Validate(); err != nil {
		return Resolved{}, fmt.Errorf("invalid Cassie configuration: %w", err)
	}
	return Resolved{Application: app, Sources: sources, Trust: executableTrust(repoPath, repoConfig)}, nil
}

func executableTrust(path string, source map[string]any) *Trust {
	if source == nil {
		return nil
	}
	executable := make(map[string]any)
	for _, key := range []string{"commands", "cleanup", "compose", "secrets"} {
		if value, exists := source[key]; exists && value != nil {
			executable[key] = value
		}
	}
	if len(executable) == 0 {
		return nil
	}
	encoded, err := json.Marshal(executable)
	if err != nil {
		return nil
	}
	digest := sha256.Sum256(encoded)
	return &Trust{Path: path, Digest: hex.EncodeToString(digest[:])}
}

func readMap(path string) (map[string]any, bool, error) {
	if path == "" {
		return nil, false, nil
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	var decoded map[string]any
	if err := yaml.Unmarshal(content, &decoded); err != nil {
		return nil, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if decoded == nil {
		decoded = make(map[string]any)
	}
	if version, ok := decoded["version"].(int); ok && version != 1 {
		return nil, false, fmt.Errorf("parse %s: unsupported version %d", path, version)
	}
	return decoded, true, nil
}

func childMap(parent map[string]any, key string) map[string]any {
	if parent == nil {
		return nil
	}
	child, _ := parent[key].(map[string]any)
	return child
}

func applicationFields(source map[string]any) map[string]any {
	result := cloneMap(source)
	for _, key := range []string{"version", "defaults", "apps", "workspace"} {
		delete(result, key)
	}
	return result
}

func matchApp(apps map[string]any, requestedName string, repository ports.Repository, dir string) (string, map[string]any) {
	if apps == nil {
		return "", nil
	}
	if requestedName != "" {
		if app, ok := apps[requestedName].(map[string]any); ok {
			if matched, _ := matches(childMap(app, "match"), repository, dir); matched {
				return requestedName, applicationFields(app)
			}
		}
		return "", nil
	}

	type candidate struct {
		name        string
		config      map[string]any
		specificity int
	}
	var candidates []candidate
	for name, raw := range apps {
		app, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		matched, specificity := matches(childMap(app, "match"), repository, dir)
		if matched {
			candidates = append(candidates, candidate{name: name, config: applicationFields(app), specificity: specificity})
		}
	}
	if len(candidates) == 0 {
		return "", nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].specificity == candidates[j].specificity {
			return candidates[i].name < candidates[j].name
		}
		return candidates[i].specificity > candidates[j].specificity
	})
	return candidates[0].name, candidates[0].config
}

func matches(match map[string]any, repository ports.Repository, dir string) (bool, int) {
	if match == nil {
		return false, 0
	}
	repo := git.NormalizeRemote(stringValue(match["repo"]))
	path := expandHome(stringValue(match["path"]))
	relativeDir := filepath.Clean(stringValue(match["dir"]))

	if repo != "" && repo != repository.Remote {
		return false, 0
	}
	if path != "" {
		absolute, err := filepath.Abs(path)
		if err != nil || !within(dir, absolute) {
			return false, 0
		}
	}
	if relativeDir != "." && relativeDir != "" {
		target := filepath.Join(repository.Root, relativeDir)
		if !within(dir, target) {
			return false, 0
		}
	}
	if repo == "" && path == "" {
		return false, 0
	}
	return true, len(relativeDir) + len(path)
}

func applicationRoot(repoRoot, currentDir string, effective map[string]any) string {
	match := childMap(effective, "match")
	if match != nil {
		if path := expandHome(stringValue(match["path"])); path != "" {
			if absolute, err := filepath.Abs(path); err == nil {
				return absolute
			}
		}
		if relative := stringValue(match["dir"]); relative != "" {
			return filepath.Join(repoRoot, relative)
		}
		if stringValue(match["repo"]) != "" {
			return repoRoot
		}
	}
	return currentDir
}

func findRepoConfig(start, repoRoot string) string {
	current := start
	for {
		candidate := filepath.Join(current, repoConfigName)
		if regularFile(candidate) {
			return candidate
		}
		if current == repoRoot || filepath.Dir(current) == current {
			return ""
		}
		current = filepath.Dir(current)
	}
}

func findWorkspaceConfig(repoRoot string) string {
	current := filepath.Dir(repoRoot)
	for {
		candidate := filepath.Join(current, repoConfigName)
		if regularFile(candidate) {
			return candidate
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func within(path, parent string) bool {
	relative, err := filepath.Rel(parent, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func sanitizeName(value string) string {
	value = strings.ToLower(value)
	var builder strings.Builder
	lastDash := false
	for _, char := range value {
		valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
		if valid {
			builder.WriteRune(char)
			lastDash = false
		} else if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
