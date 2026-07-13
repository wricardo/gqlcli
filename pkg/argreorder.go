package gqlcli

import (
	"strings"

	"github.com/urfave/cli/v2"
)

// alwaysNoValueFlags are flags urfave/cli injects automatically (not present
// in any command's own cli.Flag slice) that never consume a following token.
var alwaysNoValueFlags = map[string]bool{
	"h":       true,
	"help":    true,
	"version": true,
}

// ReorderArgsForCommand moves recognized flag tokens (and, when applicable,
// their values) ahead of positional arguments in args, which is expected to
// be a single command's own argument slice (i.e. NOT including the program
// name or the command name itself).
//
// This works around a real limitation of Go's stdlib flag package (which
// urfave/cli v2 wraps): flag parsing stops at the first non-flag token, so
// invocations like `gqlcli query '<query>' --variables '{...}'` silently
// drop --variables (and anything after it) instead of erroring — the query
// runs with no variables at all. Every documented example puts the query or
// mutation string first and flags after, so this reorder makes that
// documented usage actually work, while flags-first invocations are
// unaffected (reordering a slice that's already flags-then-positionals is a
// no-op).
func ReorderArgsForCommand(args []string, flags []cli.Flag) []string {
	takesValue := map[string]bool{}
	for name, noValue := range alwaysNoValueFlags {
		takesValue[name] = !noValue
	}
	for _, f := range flags {
		_, isBool := f.(*cli.BoolFlag)
		for _, name := range f.Names() {
			takesValue[name] = !isBool
		}
	}

	var flagTokens, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}

		flagTokens = append(flagTokens, a)
		if strings.Contains(a, "=") {
			// Value is embedded in this token (--flag=value); nothing more to consume.
			continue
		}
		name := strings.TrimLeft(a, "-")
		// Unknown flags default to "takes a value" — safer than misreading
		// their value as a positional argument, since every flag this CLI
		// defines is enumerable from the command's own Flags slice.
		if needsValue, known := takesValue[name]; !known || needsValue {
			if i+1 < len(args) {
				flagTokens = append(flagTokens, args[i+1])
				i++
			}
		}
	}

	return append(flagTokens, positional...)
}

// ReorderOSArgs reorders a full os.Args-style slice (args[0] is the program
// name) so that, for whichever top-level command is invoked, that command's
// own flags precede its positional arguments. Non-command tokens (e.g. no
// args, or top-level --help/--version) are returned unchanged. Commands that
// have their own Subcommands (e.g. "config", "op") are left untouched — their
// first positional token is a subcommand name that must stay in place, and
// reordering would misroute it behind hoisted flags.
func ReorderOSArgs(args []string, commands []*cli.Command) []string {
	if len(args) < 2 {
		return args
	}
	for _, cmd := range commands {
		if cmd.Name != args[1] && !containsString(cmd.Aliases, args[1]) {
			continue
		}
		if len(cmd.Subcommands) > 0 {
			return args
		}
		reordered := ReorderArgsForCommand(args[2:], cmd.Flags)
		out := make([]string, 0, len(args))
		out = append(out, args[0], args[1])
		out = append(out, reordered...)
		return out
	}
	return args
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
