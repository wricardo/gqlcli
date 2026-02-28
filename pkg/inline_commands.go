package gqlcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v2"
)

// InlineCommandSet builds CLI commands backed by an InlineExecutor.
// It provides query, mutation, describe, and types commands that run in-process
// without needing an HTTP server.
type InlineCommandSet struct {
	exec   *InlineExecutor
	tokens *TokenStore
	login  *LoginConfig
}

// LoginConfig configures the login/logout/whoami commands.
type LoginConfig struct {
	// Mutation is the GraphQL mutation to execute for login.
	// It must accept $email and $password string variables.
	Mutation string

	// ExtractToken extracts the JWT token from the mutation's response data map.
	ExtractToken func(data map[string]interface{}) (string, error)

	// Tokens is the store where the token will be persisted.
	// If nil, the InlineCommandSet's TokenStore is used.
	Tokens *TokenStore
}

// CommandSetOption configures an InlineCommandSet.
type CommandSetOption func(*InlineCommandSet)

// WithTokenStore attaches a TokenStore.
// The saved token is made available for the whoami and logout commands.
// To inject it into operations, use WithContextEnricher on the InlineExecutor.
func WithTokenStore(ts *TokenStore) CommandSetOption {
	return func(cs *InlineCommandSet) { cs.tokens = ts }
}

// WithLogin adds login, logout, and whoami commands to the command set.
func WithLogin(cfg LoginConfig) CommandSetOption {
	return func(cs *InlineCommandSet) {
		login := cfg
		if login.Tokens == nil {
			login.Tokens = cs.tokens
		}
		cs.login = &login
		if cs.tokens == nil {
			cs.tokens = login.Tokens
		}
	}
}

// NewInlineCommandSet creates an InlineCommandSet backed by the given executor.
func NewInlineCommandSet(exec *InlineExecutor, opts ...CommandSetOption) *InlineCommandSet {
	cs := &InlineCommandSet{exec: exec}
	for _, o := range opts {
		o(cs)
	}
	return cs
}

// Mount adds all commands to the app.
func (cs *InlineCommandSet) Mount(app *cli.App) {
	app.Commands = append(app.Commands, cs.Commands()...)
}

// Commands returns the full command list.
func (cs *InlineCommandSet) Commands() []*cli.Command {
	cmds := []*cli.Command{
		cs.queryCommand(),
		cs.mutationCommand(),
		cs.describeCommand(),
		cs.typesCommand(),
	}
	if cs.login != nil {
		cmds = append(cmds, cs.loginCommand(), cs.logoutCommand(), cs.whoamiCommand())
	}
	return cmds
}

// --- query ---

func (cs *InlineCommandSet) queryCommand() *cli.Command {
	return &cli.Command{
		Name:    "query",
		Aliases: []string{"q"},
		Usage:   "Execute a GraphQL query",
		Flags:   inlineOperationFlags("toon"),
		Action: func(c *cli.Context) error {
			op, vars, err := readInlineOperation(c)
			if err != nil {
				return err
			}
			raw, err := cs.exec.Execute(context.Background(), op, vars)
			if err != nil {
				return err
			}
			return printInlineResult(c, raw)
		},
	}
}

// --- mutation ---

func (cs *InlineCommandSet) mutationCommand() *cli.Command {
	return &cli.Command{
		Name:    "mutation",
		Aliases: []string{"m"},
		Usage:   "Execute a GraphQL mutation",
		Flags:   inlineOperationFlags("json"),
		Action: func(c *cli.Context) error {
			op, vars, err := readInlineOperation(c)
			if err != nil {
				return err
			}
			raw, err := cs.exec.Execute(context.Background(), op, vars)
			if err != nil {
				return err
			}
			return printInlineResult(c, raw)
		},
	}
}

// --- describe ---

func (cs *InlineCommandSet) describeCommand() *cli.Command {
	return &cli.Command{
		Name:      "describe",
		Aliases:   []string{"d"},
		Usage:     "Show the SDL definition of a GraphQL type",
		ArgsUsage: "TYPE_NAME",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "args", Aliases: []string{"a"}, Usage: "Expand field argument signatures"},
			&cli.BoolFlag{Name: "descriptions", Usage: "Include field/type descriptions"},
		},
		Action: func(c *cli.Context) error {
			if c.NArg() == 0 {
				return fmt.Errorf("TYPE_NAME argument is required")
			}
			typeName := c.Args().First()
			d := NewDescriber(cs.exec)
			hint, err := d.DescribeWith(context.Background(), typeName, c.Bool("args"), c.Bool("descriptions"))
			if err != nil {
				return err
			}
			fmt.Print(hint)
			return nil
		},
	}
}

// --- types ---

func (cs *InlineCommandSet) typesCommand() *cli.Command {
	return &cli.Command{
		Name:  "types",
		Usage: "List all GraphQL types in the schema",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "filter", Aliases: []string{"f"}, Usage: "Filter by name (substring match)"},
			&cli.BoolFlag{Name: "builtin", Usage: "Include built-in __ types"},
		},
		Action: func(c *cli.Context) error {
			const q = `{ __schema { types { name kind } } }`
			raw, err := cs.exec.Execute(context.Background(), q, nil)
			if err != nil {
				return err
			}

			var result map[string]interface{}
			if err := json.Unmarshal(raw, &result); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			data, _ := result["data"].(map[string]interface{})
			schema, _ := data["__schema"].(map[string]interface{})
			types, _ := schema["types"].([]interface{})

			filter := strings.ToLower(c.String("filter"))
			showBuiltin := c.Bool("builtin")

			byKind := map[string][]string{}
			for _, t := range types {
				tm, ok := t.(map[string]interface{})
				if !ok {
					continue
				}
				name, _ := tm["name"].(string)
				kind, _ := tm["kind"].(string)
				if !showBuiltin && strings.HasPrefix(name, "__") {
					continue
				}
				if filter != "" && !strings.Contains(strings.ToLower(name), filter) {
					continue
				}
				byKind[kind] = append(byKind[kind], name)
			}

			order := []string{"OBJECT", "INTERFACE", "UNION", "ENUM", "INPUT_OBJECT", "SCALAR"}
			labels := map[string]string{
				"OBJECT": "Types", "INTERFACE": "Interfaces", "UNION": "Unions",
				"ENUM": "Enums", "INPUT_OBJECT": "Inputs", "SCALAR": "Scalars",
			}
			for _, k := range order {
				names := byKind[k]
				if len(names) == 0 {
					continue
				}
				fmt.Printf("%s:\n", labels[k])
				for _, n := range names {
					fmt.Printf("  %s\n", n)
				}
			}
			return nil
		},
	}
}

// --- login ---

func (cs *InlineCommandSet) loginCommand() *cli.Command {
	return &cli.Command{
		Name:  "login",
		Usage: "Authenticate and save a session token",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "email", Aliases: []string{"e"}, Usage: "Email address", Required: true},
			&cli.StringFlag{Name: "password", Aliases: []string{"p"}, Usage: "Password", Required: true},
		},
		Action: func(c *cli.Context) error {
			cfg := cs.login
			variables := map[string]interface{}{
				"email":    c.String("email"),
				"password": c.String("password"),
			}

			raw, err := cs.exec.Execute(context.Background(), cfg.Mutation, variables)
			if err != nil {
				return fmt.Errorf("login failed: %w", err)
			}

			var result map[string]interface{}
			if err := json.Unmarshal(raw, &result); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}

			if errs, ok := result["errors"].([]interface{}); ok && len(errs) > 0 {
				if em, ok := errs[0].(map[string]interface{}); ok {
					return fmt.Errorf("login failed: %s", em["message"])
				}
				return fmt.Errorf("login failed")
			}

			data, _ := result["data"].(map[string]interface{})
			token, err := cfg.ExtractToken(data)
			if err != nil {
				return fmt.Errorf("failed to extract token: %w", err)
			}

			ts := cfg.Tokens
			if ts == nil {
				return fmt.Errorf("no token store configured")
			}
			if err := ts.Save(token); err != nil {
				return fmt.Errorf("failed to save token: %w", err)
			}

			fmt.Printf("Logged in. Token saved to %s\n", ts.dir)
			return nil
		},
	}
}

// --- logout ---

func (cs *InlineCommandSet) logoutCommand() *cli.Command {
	return &cli.Command{
		Name:  "logout",
		Usage: "Clear the saved session token",
		Action: func(c *cli.Context) error {
			ts := cs.login.Tokens
			if ts == nil || !ts.Exists() {
				fmt.Println("No active session.")
				return nil
			}
			if err := ts.Clear(); err != nil {
				return err
			}
			fmt.Println("Logged out.")
			return nil
		},
	}
}

// --- whoami ---

func (cs *InlineCommandSet) whoamiCommand() *cli.Command {
	return &cli.Command{
		Name:  "whoami",
		Usage: "Show the currently authenticated user",
		Action: func(c *cli.Context) error {
			ts := cs.login.Tokens
			if ts == nil || !ts.Exists() {
				fmt.Println("Not logged in.")
				return nil
			}
			info := ts.FormatInfo()
			if info == "" {
				fmt.Println("Token exists but could not be parsed. Try logging in again.")
			} else {
				fmt.Println(info)
			}
			return nil
		},
	}
}

// --- shared helpers ---

func inlineOperationFlags(defaultFormat string) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "query", Aliases: []string{"q"}, Usage: "GraphQL operation string"},
		&cli.StringFlag{Name: "file", Aliases: []string{"f"}, Usage: "File containing the GraphQL operation"},
		&cli.StringFlag{Name: "variables", Aliases: []string{"v"}, Usage: "Variables as JSON string"},
		&cli.StringFlag{Name: "var-file", Usage: "File containing variables as JSON"},
		&cli.StringFlag{Name: "format", Usage: "Output format: json, toon, table", Value: defaultFormat},
		&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "Write output to a file"},
	}
}

// readInlineOperation reads the GraphQL operation and variables from CLI flags/args.
func readInlineOperation(c *cli.Context) (string, map[string]interface{}, error) {
	var op string
	switch {
	case c.String("file") != "":
		b, err := os.ReadFile(c.String("file"))
		if err != nil {
			return "", nil, fmt.Errorf("failed to read file: %w", err)
		}
		op = string(b)
	case c.String("query") != "":
		op = c.String("query")
	case c.NArg() > 0:
		op = c.Args().First()
	default:
		return "", nil, fmt.Errorf("operation required: pass as argument, --query, or --file")
	}

	var vars map[string]interface{}
	switch {
	case c.String("var-file") != "":
		b, err := os.ReadFile(c.String("var-file"))
		if err != nil {
			return "", nil, fmt.Errorf("failed to read variables file: %w", err)
		}
		if err := json.Unmarshal(b, &vars); err != nil {
			return "", nil, fmt.Errorf("invalid variables JSON in file: %w", err)
		}
	case c.String("variables") != "" && c.String("variables") != "{}":
		if err := json.Unmarshal([]byte(c.String("variables")), &vars); err != nil {
			return "", nil, fmt.Errorf("invalid variables JSON: %w", err)
		}
	}

	return op, vars, nil
}

// printInlineResult formats and prints the raw GraphQL response.
// GraphQL errors are printed with their schemaHint extension if present.
func printInlineResult(c *cli.Context, raw json.RawMessage) error {
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}

	if errs, ok := result["errors"].([]interface{}); ok && len(errs) > 0 {
		for _, e := range errs {
			em, ok := e.(map[string]interface{})
			if !ok {
				continue
			}
			msg, _ := em["message"].(string)
			fmt.Printf("Error: %s\n", msg)
			if ext, ok := em["extensions"].(map[string]interface{}); ok {
				if hint, ok := ext["schemaHint"].(string); ok {
					fmt.Printf("Schema hint:\n%s\n", hint)
				}
			}
		}
		return nil
	}

	format := c.String("format")
	reg := NewFormatterRegistry()
	formatter, err := reg.Get(format)
	if err != nil {
		formatter, _ = reg.Get("json")
	}

	out, err := formatter.Format(result)
	if err != nil {
		return err
	}

	if outFile := c.String("output"); outFile != "" {
		return os.WriteFile(outFile, []byte(out), 0644)
	}

	fmt.Println(out)
	return nil
}
