package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gitadapter "github.com/andocodes/cassie/internal/adapters/git"
	"gopkg.in/yaml.v3"
)

type Workspace struct {
	Depth  int                 `yaml:"depth"`
	Ignore []string            `yaml:"ignore"`
	Groups map[string][]string `yaml:"groups"`
}

type Discovery struct {
	Applications []Resolved
	Groups       map[string][]string
}

func (r Resolver) Discover(ctx context.Context, root string) (Discovery, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return Discovery{}, err
	}
	if _, err := r.Git.Inspect(ctx, root); err == nil {
		resolved, resolveErr := r.Resolve(ctx, root, "")
		if resolveErr != nil {
			return Discovery{}, resolveErr
		}
		return Discovery{Applications: []Resolved{resolved}}, nil
	} else if !errors.Is(err, gitadapter.ErrNotRepository) {
		return Discovery{}, err
	}

	workspace, err := r.workspace(root)
	if err != nil {
		return Discovery{}, err
	}
	if workspace.Depth <= 0 {
		workspace.Depth = 2
	}
	repositories, err := discoverRepositories(root, workspace)
	if err != nil {
		return Discovery{}, err
	}
	if len(repositories) == 0 {
		resolved, resolveErr := r.Resolve(ctx, root, "")
		if resolveErr != nil {
			return Discovery{}, resolveErr
		}
		return Discovery{Applications: []Resolved{resolved}, Groups: workspace.Groups}, nil
	}

	applications := make([]Resolved, 0, len(repositories))
	for _, repository := range repositories {
		resolved, resolveErr := r.Resolve(ctx, repository, "")
		if resolveErr != nil {
			return Discovery{}, fmt.Errorf("resolve %s: %w", repository, resolveErr)
		}
		applications = append(applications, resolved)
	}
	sort.Slice(applications, func(i, j int) bool {
		if applications[i].Application.Name == applications[j].Application.Name {
			return applications[i].Application.Root < applications[j].Application.Root
		}
		return applications[i].Application.Name < applications[j].Application.Name
	})
	return Discovery{Applications: applications, Groups: workspace.Groups}, nil
}

func (r Resolver) workspace(root string) (Workspace, error) {
	effective := make(map[string]any)
	user, exists, err := readMap(r.UserPath)
	if err != nil {
		return Workspace{}, err
	}
	if exists {
		if settings := childMap(user, "workspace"); settings != nil {
			effective = merge(effective, settings)
		}
	}
	workspacePath := filepath.Join(root, repoConfigName)
	local, exists, err := readMap(workspacePath)
	if err != nil {
		return Workspace{}, err
	}
	if exists {
		if settings := childMap(local, "workspace"); settings != nil {
			effective = merge(effective, settings)
		}
	}
	encoded, err := yaml.Marshal(effective)
	if err != nil {
		return Workspace{}, err
	}
	var workspace Workspace
	decoder := yaml.NewDecoder(strings.NewReader(string(encoded)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&workspace); err != nil {
		return Workspace{}, fmt.Errorf("decode workspace configuration: %w", err)
	}
	return workspace, nil
}

func discoverRepositories(root string, workspace Workspace) ([]string, error) {
	ignored := append([]string{".git", "node_modules", "vendor", "dist", ".worktrees"}, workspace.Ignore...)
	var repositories []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		depth := 0
		if relative != "." {
			depth = len(strings.Split(relative, string(filepath.Separator)))
		}
		if depth > workspace.Depth || ignoredPath(relative, entry.Name(), ignored) {
			return filepath.SkipDir
		}
		if path != root {
			if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
				repositories = append(repositories, path)
				return filepath.SkipDir
			}
		}
		return nil
	})
	return repositories, err
}

func ignoredPath(relative, name string, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = filepath.Clean(pattern)
		if name == pattern || relative == pattern {
			return true
		}
		if matched, _ := filepath.Match(pattern, relative); matched {
			return true
		}
	}
	return false
}
