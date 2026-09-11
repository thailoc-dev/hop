package main

import (
	"context"
	"time"

	"github.com/spf13/cobra"
	"github.com/thailoc-dev/hop/internal/complete"
	"github.com/thailoc-dev/hop/internal/hopfs"
	"github.com/thailoc-dev/hop/internal/sshexec"
)

// warmTimeout bounds the background fetch. It is generous because nothing is
// waiting on it — but it is not unbounded, so an unreachable host cannot leave
// a process parked forever.
const warmTimeout = 20 * time.Second

func newWarmCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "__warm <host>",
		Short:  "Fill the completion cache for a host (internal)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			paths, err := hopfs.Default()
			if err != nil {
				return err
			}
			if err := paths.EnsureDirs(); err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), warmTimeout)
			defer cancel()

			cache := &complete.Cache{Dir: paths.CacheDir, TTL: complete.CacheTTL}
			executor := sshexec.New(paths.SocketDir)

			// Errors are silent by design: nothing is watching, and a host that
			// cannot be reached simply has no completions.
			_ = complete.Fetch(ctx, executor, cache, args[0])
			return nil
		},
	}
}
