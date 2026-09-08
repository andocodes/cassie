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

func TestApplicationNormalizesFriendlyDomainInputs(t *testing.T) {
	for _, input := range []string{"", "phoebe", "phoebe.localhost", "https://phoebe.localhost"} {
		app, err := (Application{Name: "phoebe", Domain: input}).Normalized()
		if err != nil {
			t.Fatalf("normalize %q: %v", input, err)
		}
		if app.Domain != "phoebe" || app.URL() != "https://phoebe.localhost" {
			t.Fatalf("normalize %q = domain %q, URL %q", input, app.Domain, app.URL())
		}
	}
}

func TestApplicationURLIncludesNonStandardProxyPort(t *testing.T) {
	app := Application{Name: "phoebe", Domain: "phoebe"}
	if got, want := app.URLAt(1355), "https://phoebe.localhost:1355"; got != want {
		t.Fatalf("URLAt(1355) = %q, want %q", got, want)
	}
	if got, want := app.URLAt(443), "https://phoebe.localhost"; got != want {
		t.Fatalf("URLAt(443) = %q, want %q", got, want)
	}
}

func TestSuggestedDomainNormalizesApplicationName(t *testing.T) {
	if got := SuggestedDomain("Phoebe UI"); got != "phoebe-ui" {
		t.Fatalf("SuggestedDomain = %q, want phoebe-ui", got)
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
