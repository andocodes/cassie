package system

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Paths struct {
	Config string
	Data   string
	State  string
}

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home directory: %w", err)
	}
	paths := Paths{
		Config: filepath.Join(home, ".config", "cassie", "config.yaml"),
		Data:   filepath.Join(home, ".local", "share", "cassie"),
		State:  filepath.Join(home, ".local", "state", "cassie"),
	}
	if runtime.GOOS == "windows" {
		if configDir, configErr := os.UserConfigDir(); configErr == nil {
			paths.Config = filepath.Join(configDir, "cassie", "config.yaml")
			paths.Data = filepath.Join(configDir, "cassie", "data")
			paths.State = filepath.Join(configDir, "cassie", "state")
		}
	}
	if value := os.Getenv("CASSIE_CONFIG"); value != "" {
		paths.Config = value
	}
	if value := os.Getenv("CASSIE_DATA"); value != "" {
		paths.Data = value
	}
	if value := os.Getenv("CASSIE_STATE"); value != "" {
		paths.State = value
	}
	return paths, nil
}

func (p Paths) Ensure() error {
	for _, dir := range []string{filepath.Dir(p.Config), p.Data, p.State} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create Cassie directory %s: %w", dir, err)
		}
	}
	return nil
}

func (p Paths) Database() string {
	return filepath.Join(p.State, "cassie.db")
}

func (p Paths) Tools() string {
	return filepath.Join(p.Data, "tools")
}

func (p Paths) Platform() string {
	return filepath.Join(p.Data, "platform")
}

func (p Paths) PortlessPort() string {
	return filepath.Join(p.State, "portless.port")
}
