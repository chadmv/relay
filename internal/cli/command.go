// internal/cli/command.go
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
)

// Command describes a top-level CLI subcommand.
type Command struct {
	Name  string
	Usage string
	Run   func(ctx context.Context, args []string, cfg *Config) error
}

// silentError causes Dispatch to exit 1 without printing an error message.
// Use when the failure has already been communicated via printed output.
type silentError struct{}

func (silentError) Error() string { return "" }

// Dispatch finds the command named args[0], runs it, and returns an exit code.
// Prints usage and returns 1 if args is empty or no command matches.
func Dispatch(ctx context.Context, cmds []Command, args []string, cfg *Config) int {
	if len(args) == 0 {
		printUsage(cmds)
		return 1
	}
	name := args[0]
	for _, cmd := range cmds {
		if cmd.Name == name {
			if err := cmd.Run(ctx, args[1:], cfg); err != nil {
				var se silentError
				if !errors.As(err, &se) {
					fmt.Fprintln(os.Stderr, "error:", err)
				}
				return 1
			}
			return 0
		}
	}
	fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", name)
	printUsage(cmds)
	return 1
}

func printUsage(cmds []Command) {
	fmt.Fprintln(os.Stderr, "Usage: relay <command> [flags] [args]")
	fmt.Fprintln(os.Stderr, "Commands:")
	tw := tabwriter.NewWriter(os.Stderr, 0, 0, 2, ' ', 0)
	for _, cmd := range cmds {
		fmt.Fprintf(tw, "  %s\t%s\n", cmd.Name, cmd.Usage)
	}
	_ = tw.Flush()
}
