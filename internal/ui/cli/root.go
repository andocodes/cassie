package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	composeadapter "github.com/andocodes/cassie/internal/adapters/compose"
	"github.com/andocodes/cassie/internal/adapters/config"
	gitadapter "github.com/andocodes/cassie/internal/adapters/git"
	"github.com/andocodes/cassie/internal/adapters/platform"
	processadapter "github.com/andocodes/cassie/internal/adapters/process"
	secretadapter "github.com/andocodes/cassie/internal/adapters/secrets"
	"github.com/andocodes/cassie/internal/adapters/sqlite"
	"github.com/andocodes/cassie/internal/adapters/system"
	trustadapter "github.com/andocodes/cassie/internal/adapters/trust"
	appcommand "github.com/andocodes/cassie/internal/application/command"
	"github.com/andocodes/cassie/internal/application/query"
	"github.com/andocodes/cassie/internal/domain/catalog"
	"github.com/andocodes/cassie/internal/ports"
	"github.com/andocodes/cassie/internal/ui/tui"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string { return e.err.Error() }
func (e exitError) Unwrap() error { return e.err }

func ExitCode(err error) int {
	var coded exitError
	if errors.As(err, &coded) {
		return coded.code
	}
	return 1
}

type app struct {
	version    string
	dir        string
	configPath string
	paths      system.Paths
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
}

func New(version string) (*cobra.Command, error) {
	paths, err := system.DefaultPaths()
	if err != nil {
		return nil, err
	}
	a := &app{
		version: version,
		dir:     ".",
		paths:   paths,
		stdin:   os.Stdin,
		stdout:  os.Stdout,
		stderr:  os.Stderr,
	}
	root := &cobra.Command{
		Use:           "cassie",
		Short:         "A calm front door for local development",
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       version,
		RunE: func(command *cobra.Command, _ []string) error {
			if !interactive(os.Stdin) {
				return command.Help()
			}
			return a.dashboard(command.Context())
		},
	}
	root.SetIn(a.stdin)
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.PersistentFlags().StringVar(&a.dir, "at", ".", "run as if Cassie started at this path")
	root.PersistentFlags().StringVar(&a.configPath, "config", paths.Config, "user configuration file")

	root.AddCommand(a.runCommand())
	root.AddCommand(a.linkCommand())
	root.AddCommand(a.configCommand())
	root.AddCommand(a.statusCommand())
	root.AddCommand(a.doctorCommand())
	root.AddCommand(a.trustCommand())
	root.AddCommand(a.upCommand())
	root.AddCommand(a.downCommand())
	root.AddCommand(a.logsCommand())
	root.AddCommand(a.pruneCommand())
	root.AddCommand(a.caCommand())
	root.AddCommand(a.backupCommand())
	root.AddCommand(a.restoreCommand())
	root.AddCommand(a.daemonCommand())
	return root, nil
}

func (a *app) resolver() config.Resolver {
	return config.Resolver{UserPath: a.configPath, Git: gitadapter.Inspector{}}
}

func (a *app) resolve(ctx context.Context, name string) (config.Resolved, error) {
	if name == "" {
		return a.resolver().Resolve(ctx, a.dir, "")
	}
	discovery, err := a.resolver().Discover(ctx, a.dir)
	if err == nil {
		var matches []config.Resolved
		for _, resolved := range discovery.Applications {
			if resolved.Application.Name == name {
				matches = append(matches, resolved)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return config.Resolved{}, fmt.Errorf("application name %q is ambiguous; run from its repository or use --at", name)
		}
	}
	resolved, resolveErr := a.resolver().Resolve(ctx, a.dir, name)
	if resolveErr != nil {
		return config.Resolved{}, resolveErr
	}
	if resolved.Application.Name != name {
		return config.Resolved{}, fmt.Errorf("application %q was not found from %s", name, a.dir)
	}
	return resolved, nil
}

func (a *app) dashboard(ctx context.Context) error {
	root, err := filepath.Abs(a.dir)
	if err != nil {
		return err
	}
	client, err := a.ensureDaemon(ctx)
	if err != nil {
		return err
	}
	backend := newDashboardBackend(a, root, client)
	entries, err := backend.Cached(ctx)
	if err != nil {
		return err
	}
	return tui.Run(ctx, tui.Options{Root: root, Entries: entries, Backend: backend})
}

func (a *app) runCommand() *cobra.Command {
	var all bool
	var group string
	command := &cobra.Command{
		Use:   "run [app]",
		Short: "Run an application's development flow",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if (all || group != "") && len(args) > 0 {
				return fmt.Errorf("choose an app, --all, or --group")
			}
			if all && group != "" {
				return fmt.Errorf("--all and --group cannot be used together")
			}
			if all || group != "" {
				discovery, err := a.resolver().Discover(command.Context(), a.dir)
				if err != nil {
					return err
				}
				selected := discovery.Applications
				if group != "" {
					names, exists := discovery.Groups[group]
					if !exists {
						return fmt.Errorf("workspace group %q does not exist", group)
					}
					selected = selectApps(discovery.Applications, names)
				}
				selected = runnableApps(selected)
				if len(selected) == 0 {
					return fmt.Errorf("no runnable applications selected")
				}
				return a.runMany(command.Context(), selected)
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			resolved, err := a.resolve(command.Context(), name)
			if err != nil {
				return err
			}
			return a.runResolved(command.Context(), resolved)
		},
	}
	command.Flags().BoolVar(&all, "all", false, "run every configured application in the workspace")
	command.Flags().StringVar(&group, "group", "", "run a named workspace group")
	return command
}

func (a *app) runResolved(ctx context.Context, resolved config.Resolved) error {
	return a.runMany(ctx, []config.Resolved{resolved})
}

func (a *app) runMany(ctx context.Context, resolvedApps []config.Resolved) error {
	if err := a.paths.Ensure(); err != nil {
		return err
	}
	store, err := sqlite.Open(a.paths.Database())
	if err != nil {
		return err
	}
	defer store.Close()
	for _, resolved := range resolvedApps {
		if err := a.ensureTrust(ctx, store, resolved); err != nil {
			return err
		}
	}

	portless := tool(a.paths, "portless")
	if _, err := exec.LookPath(portless); err != nil && !regularFile(portless) {
		return fmt.Errorf("Portless is not installed; run cassie up first")
	}
	infisical := tool(a.paths, "infisical")
	needsInfisical := false
	for _, resolved := range resolvedApps {
		needsInfisical = needsInfisical || resolved.Application.Secrets.Enabled()
	}
	if needsInfisical {
		if _, err := exec.LookPath(infisical); err != nil && !regularFile(infisical) {
			return fmt.Errorf("Infisical CLI is not installed; run cassie up first")
		}
	}

	if len(resolvedApps) == 1 {
		application := resolvedApps[0].Application
		handler := a.runner(store, portless, infisical, a.stdout, a.stderr)
		_, _ = fmt.Fprintf(a.stdout, "%s  %s\n", titleStyle.Render(application.Name), application.URL())
		result, err := handler.Handle(ctx, application)
		if err != nil {
			return exitError{code: result.ExitCode, err: err}
		}
		return nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		name   string
		result appcommand.RunResult
		err    error
	}
	outcomes := make(chan outcome, len(resolvedApps))
	stdout := &sharedWriter{writer: a.stdout}
	stderr := &sharedWriter{writer: a.stderr}
	colors := []lipgloss.Color{"39", "42", "81", "135", "208", "212"}
	var wait sync.WaitGroup
	for index, resolved := range resolvedApps {
		application := resolved.Application
		color := colors[index%len(colors)]
		out := newPrefixWriter(stdout, application.Name, color)
		errOut := newPrefixWriter(stderr, application.Name, color)
		wait.Add(1)
		go func() {
			defer wait.Done()
			handler := a.runner(store, portless, infisical, out, errOut)
			result, err := handler.Handle(runCtx, application)
			_ = out.Flush()
			_ = errOut.Flush()
			if err != nil {
				cancel()
			}
			outcomes <- outcome{name: application.Name, result: result, err: err}
		}()
	}
	wait.Wait()
	close(outcomes)
	var combined error
	exit := 0
	for outcome := range outcomes {
		if outcome.err != nil {
			combined = errors.Join(combined, fmt.Errorf("%s: %w", outcome.name, outcome.err))
			if exit == 0 {
				exit = outcome.result.ExitCode
			}
		}
	}
	if combined != nil {
		return exitError{code: exit, err: combined}
	}
	return nil
}

func (a *app) runner(store ports.SessionStore, portless, infisical string, stdout, stderr io.Writer) appcommand.RunApplication {
	environment := a.developmentEnvironment()
	return appcommand.RunApplication{
		Runner:   processadapter.Runner{},
		Secrets:  secretadapter.Infisical{Binary: infisical, Env: environment},
		Prepare:  composeadapter.Environment{RuntimeDir: filepath.Join(a.paths.State, "runtime")},
		Sessions: store,
		Router:   platform.Portless{Binary: portless, Env: environment},
		Env:      environment,
		Stdin:    a.stdin,
		Stdout:   stdout,
		Stderr:   stderr,
	}
}

func runnableApps(apps []config.Resolved) []config.Resolved {
	selected := make([]config.Resolved, 0, len(apps))
	for _, app := range apps {
		if len(app.Application.Commands) > 0 {
			selected = append(selected, app)
		}
	}
	return selected
}

func selectApps(apps []config.Resolved, names []string) []config.Resolved {
	selectedNames := make(map[string]struct{}, len(names))
	for _, name := range names {
		selectedNames[name] = struct{}{}
	}
	selected := make([]config.Resolved, 0, len(names))
	for _, app := range apps {
		if _, ok := selectedNames[app.Application.Name]; ok {
			selected = append(selected, app)
		}
	}
	return selected
}

func (a *app) trustCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "trust [app]",
		Short: "Trust executable configuration from the current repository",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			resolved, err := a.resolve(command.Context(), name)
			if err != nil {
				return err
			}
			if resolved.Trust == nil {
				_, _ = fmt.Fprintln(a.stdout, "No repository commands require trust.")
				return nil
			}
			if err := a.paths.Ensure(); err != nil {
				return err
			}
			store, err := sqlite.Open(a.paths.Database())
			if err != nil {
				return err
			}
			defer store.Close()
			if err := store.Trust(command.Context(), resolved.Trust.Path, resolved.Trust.Digest, time.Now()); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(a.stdout, "Trusted", resolved.Trust.Path)
			return nil
		},
	}
}

func (a *app) ensureTrust(ctx context.Context, store ports.TrustStore, resolved config.Resolved) error {
	if resolved.Trust == nil {
		return nil
	}
	trusted, err := store.Trusted(ctx, resolved.Trust.Path, resolved.Trust.Digest)
	if err != nil {
		return err
	}
	if trusted {
		return nil
	}
	if input, ok := a.stdin.(*os.File); !ok || !interactive(input) {
		return fmt.Errorf("repository commands are not trusted; inspect them and run cassie trust")
	}
	confirmed := false
	confirm := huh.NewConfirm().
		Title("Trust this repository configuration?").
		Description(commandSummary(resolved.Application)).
		Affirmative("Trust").
		Negative("Cancel").
		Value(&confirmed)
	if err := huh.NewForm(huh.NewGroup(confirm)).RunWithContext(ctx); err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("repository configuration was not trusted")
	}
	return store.Trust(ctx, resolved.Trust.Path, resolved.Trust.Digest, time.Now())
}

func commandSummary(app catalog.Application) string {
	lines := make([]string, 0, len(app.Commands)+len(app.Cleanup)+2)
	lines = append(lines, app.Name+" wants to execute:")
	for _, item := range app.Commands {
		lines = append(lines, "  "+item.Run)
	}
	if len(app.Cleanup) > 0 {
		lines = append(lines, "Cleanup:")
		for index := len(app.Cleanup) - 1; index >= 0; index-- {
			lines = append(lines, "  "+app.Cleanup[index].Run)
		}
	}
	return strings.Join(lines, "\n")
}

func (a *app) configCommand() *cobra.Command {
	group := &cobra.Command{Use: "config", Short: "Inspect Cassie configuration"}
	group.AddCommand(&cobra.Command{
		Use:   "show [app]",
		Short: "Show the effective application configuration",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			resolved, err := a.resolve(command.Context(), name)
			if err != nil {
				return err
			}
			encoded, err := yaml.Marshal(resolved.Application)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(a.stdout, "%s\nroot: %s\n", encoded, resolved.Application.Root)
			if len(resolved.Sources) > 0 {
				_, _ = fmt.Fprintln(a.stdout, "sources:")
				for _, source := range resolved.Sources {
					_, _ = fmt.Fprintln(a.stdout, "  -", source)
				}
			}
			return nil
		},
	})
	group.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print the user configuration path",
		Run: func(*cobra.Command, []string) {
			_, _ = fmt.Fprintln(a.stdout, a.configPath)
		},
	})
	return group
}

func (a *app) statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show recent Cassie sessions",
		RunE: func(command *cobra.Command, _ []string) error {
			return a.status(command.Context())
		},
	}
}

func (a *app) status(ctx context.Context) error {
	if err := a.paths.Ensure(); err != nil {
		return err
	}
	store, err := sqlite.Open(a.paths.Database())
	if err != nil {
		return err
	}
	defer store.Close()
	sessions, err := (query.RecentSessions{Store: store}).Handle(ctx, 20)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		_, _ = fmt.Fprintln(a.stdout, "No Cassie sessions yet.")
		return nil
	}
	for _, session := range sessions {
		_, _ = fmt.Fprintf(a.stdout, "%-12s %-24s %s\n", session.Status, session.App, session.StartedAt.Local().Format("2006-01-02 15:04:05"))
	}
	return nil
}

func (a *app) doctorCommand() *cobra.Command {
	var fix bool
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Check the local development platform",
		RunE: func(command *cobra.Command, _ []string) error {
			return a.doctor(command.Context(), fix)
		},
	}
	command.Flags().BoolVar(&fix, "fix", false, "install missing managed Portless and Infisical CLI tools")
	return command
}

type doctorCheck struct {
	name    string
	command string
	args    []string
	tool    bool
	version string
}

type doctorReport struct {
	failures     int
	toolFailures int
	nodeReady    bool
}

func (a *app) doctor(ctx context.Context, fix bool) error {
	report := a.runDoctorChecks(ctx, a.doctorChecks())
	if report.toolFailures > 0 {
		repair := fix
		if !repair {
			if input, ok := a.stdin.(*os.File); ok && interactive(input) {
				confirmed := false
				prompt := huh.NewConfirm().
					Title("Install Portless and Infisical CLI?").
					Description("Cassie installs pinned versions in its private data directory.").
					Affirmative("Install").
					Negative("Skip").
					Value(&confirmed)
				if err := huh.NewForm(huh.NewGroup(prompt)).RunWithContext(ctx); err != nil {
					return err
				}
				repair = confirmed
			}
		}
		if repair {
			if !report.nodeReady {
				_, _ = fmt.Fprintln(a.stdout, "Install Node.js 24 or newer, then run cassie doctor --fix.")
				return exitError{code: 1, err: fmt.Errorf("one or more checks failed")}
			}
			_, _ = fmt.Fprintln(a.stdout, "Installing pinned Portless and Infisical CLI versions…")
			if err := a.platform().InstallTools(ctx); err != nil {
				return exitError{code: 1, err: fmt.Errorf("repair managed tools: %w", err)}
			}
			toolReport := a.runDoctorChecks(ctx, a.doctorToolChecks())
			report.failures = report.failures - report.toolFailures + toolReport.failures
			report.toolFailures = toolReport.toolFailures
		} else {
			_, _ = fmt.Fprintln(a.stdout, "Run cassie doctor --fix to install Cassie's pinned tools.")
		}
	}
	if report.failures > 0 {
		return exitError{code: 1, err: fmt.Errorf("one or more checks failed")}
	}
	return nil
}

func (a *app) doctorChecks() []doctorCheck {
	return []doctorCheck{
		{name: "Docker", command: "docker", args: []string{"info", "--format", "{{json .}}"}},
		{name: "Compose", command: "docker", args: []string{"compose", "version"}},
		{name: "Node 24+", command: "node", args: []string{"--version"}},
		{name: "Portless", command: tool(a.paths, "portless"), args: []string{"--version"}, tool: true, version: platform.PortlessVersion},
		{name: "Infisical CLI", command: tool(a.paths, "infisical"), args: []string{"--version"}, tool: true, version: platform.InfisicalVersion},
	}
}

func (a *app) doctorToolChecks() []doctorCheck {
	checks := a.doctorChecks()
	return checks[len(checks)-2:]
}

func (a *app) runDoctorChecks(ctx context.Context, checks []doctorCheck) doctorReport {
	report := doctorReport{}
	for _, check := range checks {
		command := exec.CommandContext(ctx, check.command, check.args...)
		output, err := command.CombinedOutput()
		if err != nil {
			report.failures++
			if check.tool {
				report.toolFailures++
			}
			_, _ = fmt.Fprintf(a.stdout, "%s  %-16s %s\n", failStyle.Render("×"), check.name, doctorFailure(output, err))
			continue
		}
		if check.name == "Node 24+" {
			version := strings.TrimPrefix(firstLine(string(output)), "v")
			major, parseErr := strconv.Atoi(strings.Split(version, ".")[0])
			if parseErr != nil || major < 24 {
				report.failures++
				_, _ = fmt.Fprintf(a.stdout, "%s  %-16s found %s\n", failStyle.Render("×"), check.name, version)
				continue
			}
			report.nodeReady = true
		}
		detail := firstLine(string(output))
		if check.name == "Docker" {
			detail = dockerDetail(ctx, output)
		}
		if check.version != "" {
			found := reportedVersion(detail)
			if found != "" && found != check.version {
				_, _ = fmt.Fprintf(a.stdout, "%s  %-16s %s (Cassie pins %s)\n", warnStyle.Render("!"), check.name, found, check.version)
				continue
			}
		}
		_, _ = fmt.Fprintf(a.stdout, "%s  %-16s %s\n", passStyle.Render("✓"), check.name, detail)
	}
	return report
}

func doctorFailure(output []byte, err error) string {
	var missing *exec.Error
	if errors.As(err, &missing) {
		return "not installed"
	}
	if detail := firstLine(string(output)); detail != "" {
		return detail
	}
	return err.Error()
}

func reportedVersion(value string) string {
	for _, field := range strings.Fields(value) {
		candidate := strings.Trim(strings.TrimPrefix(field, "v"), ",;()[]")
		core := strings.SplitN(candidate, "-", 2)[0]
		parts := strings.Split(core, ".")
		if len(parts) != 3 {
			continue
		}
		valid := true
		for _, part := range parts {
			if _, err := strconv.Atoi(part); err != nil {
				valid = false
				break
			}
		}
		if valid {
			return candidate
		}
	}
	return ""
}

func (a *app) upCommand() *cobra.Command {
	var noLogin bool
	command := &cobra.Command{
		Use:   "up",
		Short: "Start Cassie's local platform",
		RunE: func(command *cobra.Command, _ []string) error {
			if err := a.paths.Ensure(); err != nil {
				return err
			}
			manager := a.platform()
			_, _ = fmt.Fprintln(a.stdout, "Starting Cassie's local platform…")
			if err := manager.Up(command.Context()); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(a.stdout, passStyle.Render("Ready"), platform.InfisicalURL)
			if noLogin || manager.LoginStatus(command.Context()) {
				return nil
			}
			initialized, err := manager.Initialized(command.Context())
			if err != nil {
				return err
			}
			if !initialized {
				credentials, err := a.infisicalBootstrapCredentials(command.Context())
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintln(a.stdout, "Setting up the local Infisical instance…")
				if err := manager.Bootstrap(command.Context(), credentials); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(a.stdout, passStyle.Render("Ready"), "Infisical is initialized and authenticated.")
				return nil
			}
			if input, ok := a.stdin.(*os.File); !ok || !interactive(input) {
				_, _ = fmt.Fprintln(a.stdout, "Run cassie up from an interactive terminal to sign in to Infisical.")
				return nil
			}
			_, _ = fmt.Fprintln(a.stdout, "Enter your local Infisical credentials.")
			return manager.Login(command.Context())
		},
	}
	command.Flags().BoolVar(&noLogin, "no-login", false, "start the platform without checking Infisical login")
	return command
}

func (a *app) infisicalBootstrapCredentials(ctx context.Context) (platform.BootstrapCredentials, error) {
	credentials := platform.BootstrapCredentials{
		Email:        strings.TrimSpace(os.Getenv("INFISICAL_ADMIN_EMAIL")),
		Password:     os.Getenv("INFISICAL_ADMIN_PASSWORD"),
		Organization: strings.TrimSpace(os.Getenv("INFISICAL_ADMIN_ORGANIZATION")),
	}
	if credentials.Email != "" && credentials.Password != "" && credentials.Organization != "" {
		return credentials, nil
	}
	input, ok := a.stdin.(*os.File)
	if !ok || !interactive(input) {
		return platform.BootstrapCredentials{}, fmt.Errorf("fresh Infisical setup requires INFISICAL_ADMIN_EMAIL, INFISICAL_ADMIN_PASSWORD, and INFISICAL_ADMIN_ORGANIZATION")
	}
	if credentials.Email == "" {
		credentials.Email = gitEmail(ctx)
	}
	if credentials.Organization == "" {
		credentials.Organization = "Cassie"
	}
	fields := []huh.Field{
		huh.NewInput().Title("Admin email").Value(&credentials.Email).Validate(required("admin email")),
		huh.NewInput().Title("Organization").Value(&credentials.Organization).Validate(required("organization")),
	}
	if credentials.Password == "" {
		fields = append(fields, huh.NewInput().Title("Admin password").EchoMode(huh.EchoModePassword).Value(&credentials.Password).Validate(minimumLength("admin password", 14)))
	}
	if err := huh.NewForm(huh.NewGroup(fields...)).RunWithContext(ctx); err != nil {
		return platform.BootstrapCredentials{}, err
	}
	return credentials, nil
}

func gitEmail(ctx context.Context) string {
	output, err := exec.CommandContext(ctx, "git", "config", "--global", "user.email").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func minimumLength(name string, length int) func(string) error {
	return func(value string) error {
		if len(value) < length {
			return fmt.Errorf("%s must be at least %d characters", name, length)
		}
		return nil
	}
}

func (a *app) downCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop Cassie's local platform without deleting data",
		RunE: func(command *cobra.Command, _ []string) error {
			return a.platform().Down(command.Context())
		},
	}
}

func (a *app) logsCommand() *cobra.Command {
	var follow bool
	command := &cobra.Command{
		Use:   "logs",
		Short: "Show platform logs",
		RunE: func(command *cobra.Command, _ []string) error {
			return a.platform().Logs(command.Context(), follow)
		},
	}
	command.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	return command
}

func (a *app) pruneCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Remove stale routes without deleting application data",
		RunE: func(command *cobra.Command, _ []string) error {
			if err := a.platform().Prune(command.Context()); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(a.stdout, "Pruned stale Portless routes. Volumes were untouched.")
			return nil
		},
	}
}

func (a *app) platform() platform.Manager {
	return platform.Manager{
		DataDir: a.paths.Platform(),
		Tools:   a.paths.Tools(),
		Stdin:   a.stdin,
		Stdout:  a.stdout,
		Stderr:  a.stderr,
		Env:     a.developmentEnvironment(),
	}
}

func (a *app) backupCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "backup [archive]",
		Short: "Back up Infisical data and encryption configuration",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			path, err := a.platform().Backup(command.Context(), target)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(a.stdout, "Backup written to", path)
			_, _ = fmt.Fprintln(a.stdout, "The archive contains encrypted secrets and platform keys; store it securely.")
			return nil
		},
	}
}

func (a *app) restoreCommand() *cobra.Command {
	var confirmed bool
	command := &cobra.Command{
		Use:   "restore <archive>",
		Short: "Restore Infisical from a Cassie backup",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if !confirmed {
				if input, ok := a.stdin.(*os.File); !ok || !interactive(input) {
					return fmt.Errorf("restore requires --yes in a non-interactive terminal")
				}
				prompt := huh.NewConfirm().
					Title("Replace the current Infisical database?").
					Description("Cassie will create a safety backup first. Application volumes are otherwise untouched.").
					Affirmative("Restore").
					Negative("Cancel").
					Value(&confirmed)
				if err := huh.NewForm(huh.NewGroup(prompt)).RunWithContext(command.Context()); err != nil {
					return err
				}
			}
			if !confirmed {
				return fmt.Errorf("restore cancelled")
			}
			manager := a.platform()
			safety, err := manager.Backup(command.Context(), "")
			if err != nil {
				return fmt.Errorf("create pre-restore backup: %w", err)
			}
			_, _ = fmt.Fprintln(a.stdout, "Safety backup:", safety)
			if err := manager.Restore(command.Context(), args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(a.stdout, "Infisical restored from", args[0])
			return nil
		},
	}
	command.Flags().BoolVarP(&confirmed, "yes", "y", false, "confirm replacement of the current Infisical database")
	return command
}

func (a *app) caCommand() *cobra.Command {
	group := &cobra.Command{Use: "ca", Short: "Manage corporate certificate authorities"}
	var systemTrust bool
	var restartDocker bool
	add := &cobra.Command{
		Use:   "add <certificate.pem>",
		Short: "Validate and add a corporate CA",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			store := a.certificateStore()
			certificates, err := store.Add(args[0])
			if err != nil {
				return err
			}
			for _, certificate := range certificates {
				printCertificate(a.stdout, certificate)
			}
			if runtime.GOOS == "darwin" && !systemTrust {
				if input, ok := a.stdin.(*os.File); ok && interactive(input) {
					confirm := huh.NewConfirm().Title("Trust this CA system-wide for Docker Desktop?").Value(&systemTrust)
					if err := huh.NewForm(huh.NewGroup(confirm)).RunWithContext(command.Context()); err != nil {
						return err
					}
				}
			}
			if systemTrust {
				if err := installSystemCA(command.Context(), certificates[0].Path, a.stdin, a.stdout, a.stderr); err != nil {
					return err
				}
				if !restartDocker {
					if input, ok := a.stdin.(*os.File); ok && interactive(input) {
						prompt := huh.NewConfirm().Title("Restart Docker Desktop now?").Value(&restartDocker)
						if err := huh.NewForm(huh.NewGroup(prompt)).RunWithContext(command.Context()); err != nil {
							return err
						}
					}
				}
			}
			if restartDocker && !systemTrust {
				return fmt.Errorf("--restart-docker requires --system or system trust confirmation")
			}
			if restartDocker {
				return restartDockerDesktop(command.Context(), a.stdout, a.stderr)
			}
			_, _ = fmt.Fprintln(a.stdout, "Cassie will expose this bundle through NODE_EXTRA_CA_CERTS.")
			return nil
		},
	}
	add.Flags().BoolVar(&systemTrust, "system", false, "install the CA in the macOS system trust store")
	add.Flags().BoolVar(&restartDocker, "restart-docker", false, "restart Docker Desktop after changing system trust")
	group.AddCommand(add)
	group.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "List managed certificate authorities",
		RunE: func(*cobra.Command, []string) error {
			certificates, err := a.certificateStore().List()
			if err != nil {
				return err
			}
			if len(certificates) == 0 {
				_, _ = fmt.Fprintln(a.stdout, "No managed certificate authorities.")
				return nil
			}
			for _, certificate := range certificates {
				printCertificate(a.stdout, certificate)
			}
			return nil
		},
	})
	var removeSystem bool
	remove := &cobra.Command{
		Use:   "remove <fingerprint>",
		Short: "Remove a managed certificate authority",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			certificate, err := a.certificateStore().Remove(args[0])
			if err != nil {
				return err
			}
			if removeSystem {
				if err := removeSystemCA(command.Context(), certificate.Fingerprint, a.stdin, a.stdout, a.stderr); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintln(a.stdout, "Removed", certificate.Subject)
			return nil
		},
	}
	remove.Flags().BoolVar(&removeSystem, "system", false, "also remove the CA from the macOS system trust store")
	group.AddCommand(remove)
	return group
}

func (a *app) certificateStore() trustadapter.Store {
	return trustadapter.Store{Dir: filepath.Join(a.paths.Data, "certs")}
}

func (a *app) developmentEnvironment() map[string]string {
	noProxy := mergeNoProxy(os.Getenv("NO_PROXY"))
	values := map[string]string{
		"NO_PROXY":           noProxy,
		"no_proxy":           noProxy,
		"PORTLESS_STATE_DIR": filepath.Join(a.paths.State, "portless"),
	}
	store := a.certificateStore()
	if store.HasBundle() {
		values["NODE_EXTRA_CA_CERTS"] = store.Bundle()
	}
	return values
}

func printCertificate(writer io.Writer, certificate trustadapter.Certificate) {
	_, _ = fmt.Fprintf(writer, "%s\n  issuer: %s\n  expires: %s\n  sha256: %s\n",
		certificate.Subject,
		certificate.Issuer,
		certificate.Expires.Local().Format("2006-01-02 15:04:05 MST"),
		certificate.Fingerprint,
	)
}

func installSystemCA(ctx context.Context, path string, stdin io.Reader, stdout, stderr io.Writer) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("automatic system trust installation is currently supported on macOS only")
	}
	command := exec.CommandContext(ctx, "sudo", "security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", "/Library/Keychains/System.keychain", path)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("install system certificate: %w", err)
	}
	return nil
}

func removeSystemCA(ctx context.Context, fingerprint string, stdin io.Reader, stdout, stderr io.Writer) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("automatic system trust removal is currently supported on macOS only")
	}
	fingerprint = strings.ReplaceAll(fingerprint, ":", "")
	command := exec.CommandContext(ctx, "sudo", "security", "delete-certificate", "-Z", fingerprint, "/Library/Keychains/System.keychain")
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("remove system certificate: %w", err)
	}
	return nil
}

func restartDockerDesktop(ctx context.Context, stdout, stderr io.Writer) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("automatic Docker Desktop restart is currently supported on macOS only")
	}
	quit := exec.CommandContext(ctx, "osascript", "-e", `quit app "Docker"`)
	quit.Stdout = stdout
	quit.Stderr = stderr
	if err := quit.Run(); err != nil {
		return fmt.Errorf("stop Docker Desktop: %w", err)
	}
	start := exec.CommandContext(ctx, "open", "-a", "Docker")
	start.Stdout = stdout
	start.Stderr = stderr
	if err := start.Run(); err != nil {
		return fmt.Errorf("start Docker Desktop: %w", err)
	}
	return nil
}

func mergeNoProxy(current string) string {
	values := []string{"localhost", "127.0.0.1", ".localhost"}
	seen := make(map[string]struct{}, len(values)+4)
	var merged []string
	for _, value := range append(strings.Split(current, ","), values...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		merged = append(merged, value)
	}
	return strings.Join(merged, ",")
}

func (a *app) linkCommand() *cobra.Command {
	var userScope bool
	var repoScope bool
	command := &cobra.Command{
		Use:   "link",
		Short: "Connect the current application to Cassie",
		RunE: func(command *cobra.Command, _ []string) error {
			if userScope && repoScope {
				return fmt.Errorf("--user and --repo cannot be used together")
			}
			scope := ""
			if userScope {
				scope = "user"
			}
			if repoScope {
				scope = "repo"
			}
			return a.linkWithScope(command.Context(), scope)
		},
	}
	command.Flags().BoolVar(&userScope, "user", false, "save outside the repository in user configuration")
	command.Flags().BoolVar(&repoScope, "repo", false, "save a shareable .cassie.yaml in the repository")
	return command
}

func (a *app) link(ctx context.Context) error {
	return a.linkWithScope(ctx, "")
}

func (a *app) linkWithScope(ctx context.Context, forcedScope string) error {
	dir, err := filepath.Abs(a.dir)
	if err != nil {
		return err
	}
	repository, gitErr := (gitadapter.Inspector{}).Inspect(ctx, dir)
	if gitErr != nil {
		repository = ports.Repository{Root: dir}
	}
	name := sanitize(filepath.Base(dir))
	commands := inferCommand(dir)
	scope := forcedScope
	if scope == "" {
		scope = "user"
	}

	fields := []huh.Field{}
	if forcedScope == "" {
		fields = append(fields, huh.NewSelect[string]().Title("Save application").Options(
			huh.NewOption("User configuration", "user"),
			huh.NewOption("Repository .cassie.yaml", "repo"),
		).Value(&scope))
	}
	if len(fields) > 0 {
		form := huh.NewForm(huh.NewGroup(fields...))
		if err := form.RunWithContext(ctx); err != nil {
			return err
		}
	}
	application := catalog.Application{
		Name:     name,
		Root:     dir,
		Commands: commandLines(commands),
	}
	application, err = application.Normalized()
	if err != nil {
		return err
	}
	if err := application.Validate(); err != nil {
		return err
	}

	writer := config.Writer{UserPath: a.configPath}
	switch scope {
	case "user":
		match := config.Match{Repo: repository.Remote}
		if repository.Remote == "" {
			match.Path = dir
		} else if relative, relErr := filepath.Rel(repository.Root, dir); relErr == nil {
			match.Dir = relative
		}
		if err := writer.SaveUser(name, match, application); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(a.stdout, "Linked %s in %s\n", name, a.configPath)
	case "repo":
		path, err := writer.SaveRepo(dir, application)
		if err != nil {
			return err
		}
		if err := a.trustLinkedRepository(ctx); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(a.stdout, "Linked %s in %s\n", name, path)
	default:
		return fmt.Errorf("scope must be user or repo")
	}
	_, _ = fmt.Fprintln(a.stdout, application.URL())
	if len(application.Commands) == 0 {
		_, _ = fmt.Fprintln(a.stdout, "No run command detected. Add commands in Cassie configuration before running this application.")
	}
	return nil
}

func (a *app) trustLinkedRepository(ctx context.Context) error {
	resolved, err := a.resolver().Resolve(ctx, a.dir, "")
	if err != nil || resolved.Trust == nil {
		return err
	}
	if err := a.paths.Ensure(); err != nil {
		return err
	}
	store, err := sqlite.Open(a.paths.Database())
	if err != nil {
		return err
	}
	defer store.Close()
	return store.Trust(ctx, resolved.Trust.Path, resolved.Trust.Digest, time.Now())
}

func inferCommand(dir string) string {
	for _, candidate := range []struct {
		file    string
		command string
	}{
		{"compose.yaml", "docker compose up"},
		{"compose.yml", "docker compose up"},
		{"docker-compose.yaml", "docker compose up"},
		{"docker-compose.yml", "docker compose up"},
		{"bun.lock", "bun run dev"},
		{"bun.lockb", "bun run dev"},
		{"pnpm-lock.yaml", "pnpm dev"},
		{"yarn.lock", "yarn dev"},
		{"package-lock.json", "npm run dev"},
	} {
		if regularFile(filepath.Join(dir, candidate.file)) {
			return candidate.command
		}
	}
	if regularFile(filepath.Join(dir, "package.json")) {
		return "npm run dev"
	}
	return ""
}

func commandLines(value string) []catalog.Command {
	var commands []catalog.Command
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			commands = append(commands, catalog.Command{Run: line})
		}
	}
	return commands
}

func required(name string) func(string) error {
	return func(value string) error {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
		return nil
	}
}

func tool(paths system.Paths, name string) string {
	filename := name
	if runtime.GOOS == "windows" {
		filename += ".cmd"
	}
	managed := filepath.Join(paths.Tools(), "node_modules", ".bin", filename)
	if regularFile(managed) {
		return managed
	}
	return name
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func sanitize(value string) string {
	value = strings.ToLower(value)
	var output strings.Builder
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' {
			output.WriteRune(character)
		} else {
			output.WriteByte('-')
		}
	}
	return strings.Trim(output.String(), "-")
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return value[:index]
	}
	return value
}

func interactive(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	passStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	failStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)
