package runtime_test

import (
	"testing"

	"github.com/andocodes/cassie/internal/domain/runtime"
)

func TestProcessSpecRejectsDirectoryOutsideApplicationRoot(t *testing.T) {
	spec := runtime.ProcessSpec{
		ID: "session-1", App: "phoebe-ui", Root: "/work/phoebe-ui",
		Command: "pnpm dev", Dir: "../phoebe-api",
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected escaping process directory to fail")
	}
}
