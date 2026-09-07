package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var domainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var value string
	if err := node.Decode(&value); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", value, err)
	}
	d.Duration = parsed
	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	return d.String(), nil
}

type Command struct {
	Run string `yaml:"run" json:"run"`
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`
}

func (c *Command) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		c.Run = strings.TrimSpace(node.Value)
		return nil
	case yaml.MappingNode:
		type command Command
		var decoded command
		if err := node.Decode(&decoded); err != nil {
			return err
		}
		decoded.Run = strings.TrimSpace(decoded.Run)
		*c = Command(decoded)
		return nil
	default:
		return fmt.Errorf("command must be a string or an object with run and optional dir")
	}
}

func (c Command) MarshalYAML() (any, error) {
	if c.Dir == "" {
		return c.Run, nil
	}
	type command Command
	return command(c), nil
}

type SecretBinding struct {
	Project     string `yaml:"project,omitempty" json:"project,omitempty"`
	Environment string `yaml:"environment,omitempty" json:"environment,omitempty"`
	Path        string `yaml:"path,omitempty" json:"path,omitempty"`
}

func (s SecretBinding) Enabled() bool {
	return s.Project != ""
}

type Compose struct {
	Services []string `yaml:"services,omitempty" json:"services,omitempty"`
}

type Application struct {
	Name     string        `yaml:"name" json:"name"`
	Domain   string        `yaml:"domain" json:"domain"`
	Port     int           `yaml:"port,omitempty" json:"port,omitempty"`
	Root     string        `yaml:"-" json:"root"`
	Grace    Duration      `yaml:"grace,omitempty" json:"grace"`
	Commands []Command     `yaml:"commands" json:"commands"`
	Cleanup  []Command     `yaml:"cleanup,omitempty" json:"cleanup,omitempty"`
	Secrets  SecretBinding `yaml:"secrets,omitempty" json:"secrets,omitempty"`
	Compose  Compose       `yaml:"compose,omitempty" json:"compose,omitempty"`
}

func (a Application) Validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("application name is required")
	}
	if !validDomain(a.Domain) || strings.HasSuffix(a.Domain, ".localhost") || a.Domain == "infisical" {
		return fmt.Errorf("domain %q must be a bare lowercase hostname such as %q", a.Domain, a.Name)
	}
	if a.Port < 0 || a.Port > 65535 {
		return fmt.Errorf("port must be between 0 and 65535")
	}
	for _, command := range append(append([]Command{}, a.Commands...), a.Cleanup...) {
		if command.Run == "" {
			return fmt.Errorf("commands require a non-empty run value")
		}
		cleaned := filepath.Clean(command.Dir)
		if filepath.IsAbs(command.Dir) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return fmt.Errorf("command dir %q must be relative to the application root", command.Dir)
		}
	}
	return nil
}

func validDomain(domain string) bool {
	if domain == "" || len(domain) > 253 {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || !domainPattern.MatchString(label) || strings.Contains(label, ".") {
			return false
		}
	}
	return true
}

func (a Application) ValidateRunnable() error {
	if err := a.Validate(); err != nil {
		return err
	}
	if len(a.Commands) == 0 {
		return fmt.Errorf("application %q has no commands", a.Name)
	}
	return nil
}

func (a Application) URL() string {
	return "https://" + a.Domain + ".localhost"
}

func SuggestedDomain(name string) string {
	const maxLabelLength = 63
	const hashLength = 8

	name = strings.Trim(name, "-")
	if len(name) <= maxLabelLength {
		return name
	}
	digest := sha256.Sum256([]byte(name))
	suffix := hex.EncodeToString(digest[:])[:hashLength]
	prefix := strings.TrimRight(name[:maxLabelLength-hashLength-1], "-")
	return prefix + "-" + suffix
}
