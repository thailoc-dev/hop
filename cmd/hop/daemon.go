package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/locnguyen/hop/internal/sshexec"
	"github.com/locnguyen/hop/internal/supervisor"
	"github.com/locnguyen/hop/internal/sysprobe"
	"github.com/spf13/cobra"
)

func newDaemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "__daemon",
		Short:  "Run the tunnel supervisor (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   func(*cobra.Command, []string) error { return runDaemon() },
	}
}

func runDaemon() error {
	paths, err := hopfs.Default()
	if err != nil {
		return err
	}
	if err := paths.EnsureDirs(); err != nil {
		return err
	}

	log.SetPrefix("hop-daemon ")
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)

	listener, err := net.Listen("unix", paths.ControlSock)
	if err != nil {
		return fail(exitInternal, "listen on %s: %v", paths.ControlSock, err)
	}
	defer func() { _ = listener.Close() }()

	sup := supervisor.New(supervisor.Config{
		StatePath: paths.StateFile,
		Exec:      sshexec.New(paths.SocketDir),
		Probe:     sysprobe.New(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	go func() { _ = control.Serve(listener, sup) }()

	done := make(chan struct{})
	go func() { _ = sup.Run(ctx); close(done) }()

	select {
	case sig := <-signals:
		log.Printf("received %s, stopping tunnels", sig)
	case <-sup.Empty():
		log.Print("last tunnel removed, exiting")
	}

	cancel()
	<-done
	return nil
}
