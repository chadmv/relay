// internal/cli/command.go
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
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

// reorderArgs moves flag arguments before positional arguments so that flags
// can appear anywhere in the command line (e.g. "cmd <arg> --flag" works the
// same as "cmd --flag <arg>"). fs is used to determine which flags take a
// value argument so that those values are not mistaken for positional args.
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	var flagArgs, posArgs []string
	i := 0
	for i < len(args) {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			posArgs = append(posArgs, arg)
			i++
			continue
		}
		// Extract the flag name, stripping leading dashes and any =value suffix.
		name := strings.TrimLeft(arg, "-")
		if eq := strings.Index(name, "="); eq >= 0 {
			name = name[:eq]
		}
		flagArgs = append(flagArgs, arg)
		// If the flag is known, not boolean, and has no inline =value, consume
		// the next arg as its value.
		f := fs.Lookup(name)
		if f != nil && !isBoolFlag(f) && !strings.Contains(arg, "=") && i+1 < len(args) {
			flagArgs = append(flagArgs, args[i+1])
			i += 2
			continue
		}
		i++
	}
	return append(flagArgs, posArgs...)
}

func isBoolFlag(f *flag.Flag) bool {
	type boolFlagger interface{ IsBoolFlag() bool }
	if bf, ok := f.Value.(boolFlagger); ok {
		return bf.IsBoolFlag()
	}
	return false
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
