package cli

import (
	"path/filepath"

	daemonadapter "github.com/andocodes/cassie/internal/adapters/daemon"
	processadapter "github.com/andocodes/cassie/internal/adapters/process"
	"github.com/andocodes/cassie/internal/adapters/processlog"
	"github.com/andocodes/cassie/internal/adapters/sqlite"
	"github.com/spf13/cobra"
)

const daemonLogLimit = 4 << 20

func (a *app) daemonCommand() *cobra.Command {
	group := &cobra.Command{
		Use:    "daemon",
		Short:  "Run Cassie's local process service",
		Hidden: true,
	}
	group.AddCommand(&cobra.Command{
		Use:    "serve",
		Short:  "Serve local Cassie clients",
		Hidden: true,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := a.paths.Ensure(); err != nil {
				return err
			}
			store, err := sqlite.Open(a.paths.Database())
			if err != nil {
				return err
			}
			defer store.Close()
			logs := processlog.New(filepath.Join(a.paths.State, "logs"), daemonLogLimit)
			supervisor := processadapter.NewSupervisor(store, logs)
			return daemonadapter.NewServer(a.paths.State, supervisor).Serve(command.Context())
		},
	})
	return group
}
