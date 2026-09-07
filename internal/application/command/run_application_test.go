package command_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/andocodes/cassie/internal/application/command"
	"github.com/andocodes/cassie/internal/domain/catalog"
	"github.com/andocodes/cassie/internal/domain/runtime"
	"github.com/andocodes/cassie/internal/ports"
)

type fakeRunner struct {
	runs   []string
	failAt string
}

func (f *fakeRunner) Run(_ context.Context, process ports.Process) error {
	f.runs = append(f.runs, process.Command.Run)
	if process.Command.Run == f.failAt {
		return errors.New("failed")
	}
	return nil
}

type fakeSecrets struct {
	values map[string]string
}

type fakePrepare struct {
	cleaned bool
}

func (f *fakePrepare) Prepare(_ context.Context, _ string, _ catalog.Application, values map[string]string) (ports.PreparedEnvironment, error) {
	values["PREPARED"] = "yes"
	return ports.PreparedEnvironment{
		Values: values,
		Cleanup: func() error {
			f.cleaned = true
			return nil
		},
	}, nil
}

func (f fakeSecrets) Load(context.Context, catalog.SecretBinding) (map[string]string, error) {
	return f.values, nil
}

type fakeSessions struct {
	started  runtime.Session
	status   runtime.Status
	exitCode int
}

func (f *fakeSessions) Start(_ context.Context, session runtime.Session) error {
	f.started = session
	return nil
}

func (f *fakeSessions) Finish(_ context.Context, _ string, status runtime.Status, exitCode int, _ time.Time) error {
	f.status = status
	f.exitCode = exitCode
	return nil
}

func (f *fakeSessions) Recent(context.Context, int) ([]runtime.Session, error) {
	return nil, nil
}

type fakeRouter struct {
	aliases []string
	removes []string
}

func (f *fakeRouter) Wrap(_ catalog.Application, item catalog.Command) catalog.Command {
	item.Run = "portless " + item.Run
	return item
}

func (f *fakeRouter) Alias(_ context.Context, domain string, _ int) error {
	f.aliases = append(f.aliases, domain)
	return nil
}

func (f *fakeRouter) Remove(_ context.Context, domain string) error {
	f.removes = append(f.removes, domain)
	return nil
}

func TestRunApplicationExecutesCommandsAndCleanupInReverse(t *testing.T) {
	runner := &fakeRunner{}
	sessions := &fakeSessions{}
	router := &fakeRouter{}
	prepare := &fakePrepare{}
	handler := command.RunApplication{
		Runner:   runner,
		Secrets:  fakeSecrets{values: map[string]string{"TOKEN": "secret"}},
		Prepare:  prepare,
		Sessions: sessions,
		Router:   router,
		Now:      func() time.Time { return time.Unix(1, 0) },
		Stdin:    strings.NewReader(""),
		Stdout:   io.Discard,
		Stderr:   io.Discard,
	}
	app := catalog.Application{
		Name:   "atlas",
		Domain: "atlas",
		Root:   "/tmp/atlas",
		Commands: []catalog.Command{
			{Run: "generate"},
			{Run: "dev"},
		},
		Cleanup: []catalog.Command{
			{Run: "first-cleanup"},
			{Run: "second-cleanup"},
		},
	}

	result, err := handler.Handle(context.Background(), app)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []string{"generate", "portless dev", "second-cleanup", "first-cleanup"}
	if !reflect.DeepEqual(runner.runs, want) {
		t.Fatalf("execution order = %#v, want %#v", runner.runs, want)
	}
	if result.ExitCode != 0 || sessions.status != runtime.StatusSucceeded {
		t.Fatalf("unexpected result %#v and status %q", result, sessions.status)
	}
	if !prepare.cleaned {
		t.Fatal("prepared runtime files were not cleaned")
	}
}

func TestRunApplicationStopsAfterFailureButStillCleansUp(t *testing.T) {
	runner := &fakeRunner{failAt: "prepare"}
	sessions := &fakeSessions{}
	router := &fakeRouter{}
	handler := command.RunApplication{
		Runner:   runner,
		Secrets:  fakeSecrets{},
		Sessions: sessions,
		Router:   router,
		Stdout:   io.Discard,
		Stderr:   io.Discard,
	}
	app := catalog.Application{
		Name:     "atlas",
		Domain:   "atlas",
		Port:     3000,
		Commands: []catalog.Command{{Run: "prepare"}, {Run: "dev"}},
		Cleanup:  []catalog.Command{{Run: "cleanup"}},
	}

	result, err := handler.Handle(context.Background(), app)
	if err == nil {
		t.Fatal("expected failure")
	}
	want := []string{"prepare", "cleanup"}
	if !reflect.DeepEqual(runner.runs, want) {
		t.Fatalf("execution order = %#v, want %#v", runner.runs, want)
	}
	if !reflect.DeepEqual(router.aliases, []string{"atlas"}) || !reflect.DeepEqual(router.removes, []string{"atlas"}) {
		t.Fatalf("route lifecycle = aliases %#v removes %#v", router.aliases, router.removes)
	}
	if result.ExitCode != 1 || sessions.status != runtime.StatusFailed {
		t.Fatalf("unexpected result %#v and status %q", result, sessions.status)
	}
}
