package git

import "testing"

func TestNormalizeRemote(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{"git@github.com:Acme/Atlas.git", "github.com/acme/atlas"},
		{"https://github.com/Acme/Atlas.git", "github.com/acme/atlas"},
		{"ssh://git@gitlab.example.com/acme/atlas.git", "gitlab.example.com/acme/atlas"},
	} {
		if got := NormalizeRemote(test.input); got != test.want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}
