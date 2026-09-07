package catalog

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestApplicationRejectsUnsafeConfiguration(t *testing.T) {
	for _, test := range []struct {
		name string
		app  Application
	}{
		{"localhost suffix", Application{Name: "web", Domain: "web.localhost"}},
		{"reserved platform domain", Application{Name: "web", Domain: "infisical"}},
		{"directory traversal", Application{Name: "web", Domain: "web", Commands: []Command{{Run: "dev", Dir: "../other"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.app.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCommandYAMLAcceptsSimpleAndStructuredForms(t *testing.T) {
	app := Application{Name: "web", Domain: "web"}
	if err := yaml.Unmarshal([]byte("commands:\n  - bun run dev\n  - run: go run .\n    dir: api\n"), &app); err != nil {
		t.Fatal(err)
	}
	if len(app.Commands) != 2 || app.Commands[0].Run != "bun run dev" || app.Commands[1].Dir != "api" {
		t.Fatalf("unexpected commands: %#v", app.Commands)
	}
	if strings.TrimSpace(app.Commands[1].Run) != "go run ." {
		t.Fatalf("unexpected structured command: %#v", app.Commands[1])
	}
}

func TestSuggestedDomainShortensLongNamesWithoutCollisions(t *testing.T) {
	first := SuggestedDomain("reuters-devops-enterprise-news-phoebe-infrastructure-atlas-session")
	second := SuggestedDomain("reuters-devops-enterprise-news-phoebe-infrastructure-atlas-staging")

	if len(first) > 63 || len(second) > 63 {
		t.Fatalf("suggested domains exceed one DNS label: %q, %q", first, second)
	}
	if first == second {
		t.Fatalf("distinct names produced the same domain %q", first)
	}
	if !validDomain(first) || !validDomain(second) {
		t.Fatalf("suggested domains are invalid: %q, %q", first, second)
	}
}
