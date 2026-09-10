package main

import (
	"context"
	"os"
	"os/exec"
	"syscall"

	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/locnguyen/hop/internal/sshexec"
	"github.com/spf13/cobra"
)

// sshArgvFor builds the exact argv a passthrough command will exec.
//
// These five commands exist so that deleting the old bash script loses
// nothing. Their argument order matches it exactly, including scp options
// appearing before the host — `hop push -r host ./dist /tmp/dist` is in the
// script's own documented examples.
func sshArgvFor(name string, args []string) ([]string, error) {
	switch name {
	case "run":
		if len(args) < 2 {
			return nil, fail(exitUsage, "run requires a host and a command")
		}
		return append([]string{"ssh"}, args...), nil

	case "shell":
		if len(args) != 1 {
			return nil, fail(exitUsage, "shell requires exactly one host")
		}
		return []string{"ssh", args[0]}, nil

	case "push", "pull":
		if len(args) < 3 {
			return nil, fail(exitUsage,
				"%s requires a host, a source and a destination", name)
		}
		options := args[:len(args)-3]
		host, first, second := args[len(args)-3], args[len(args)-2], args[len(args)-1]

		argv := append([]string{"scp"}, options...)
		if name == "push" {
			return append(argv, first, host+":"+second), nil
		}
		return append(argv, host+":"+first, second), nil

	default:
		return nil, fail(exitInternal, "unknown passthrough %q", name)
	}
}

// execPassthrough replaces this process with ssh or scp, so signals, exit
// codes and terminal control behave exactly as if it had been typed directly.
func execPassthrough(name string, args []string) error {
	argv, err := sshArgvFor(name, args)
	if err != nil {
		return err
	}

	binary, err := exec.LookPath(argv[0])
	if err != nil {
		return fail(exitInternal, "%s not found in PATH", argv[0])
	}
	return syscall.Exec(binary, argv, os.Environ())
}

func passthroughCmd(name, use, short string) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		DisableFlagParsing: true, // flags belong to ssh/scp, not to hop
		RunE: func(_ *cobra.Command, args []string) error {
			return execPassthrough(name, args)
		},
	}
}

func newRunCmd() *cobra.Command {
	return passthroughCmd("run", "run <host> <command...>", "Run a command on a host")
}

func newShellCmd() *cobra.Command {
	return passthroughCmd("shell", "shell <host>", "Open an interactive shell on a host")
}

func newPushCmd() *cobra.Command {
	return passthroughCmd("push", "push [scp-options...] <host> <local> <remote>", "Copy a file to a host")
}

func newPullCmd() *cobra.Command {
	return passthroughCmd("pull", "pull [scp-options...] <host> <remote> <local>", "Copy a file from a host")
}

func newDockerIPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "docker-ip <host> <container>",
		Short: "Print a container's IP address on a host",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := hopfs.Default()
			if err != nil {
				return err
			}
			if err := paths.EnsureDirs(); err != nil {
				return err
			}

			executor := sshexec.New(paths.SocketDir)
			ip, err := executor.ContainerIP(context.Background(), args[0], args[1])
			if err != nil {
				return fail(exitFatal, "%v", err)
			}
			cmd.Println(ip)
			return nil
		},
	}
}
