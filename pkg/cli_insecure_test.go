package gqlcli

import (
	"flag"
	"testing"

	"github.com/urfave/cli/v2"
)

func TestApplyEnvConfigSetsInsecureFromFlag(t *testing.T) {
	set := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := []cli.Flag{
		&cli.StringFlag{Name: "env"},
		&cli.StringFlag{Name: "url"},
		&cli.BoolFlag{Name: "debug"},
		insecureFlag(),
		headerFlag(),
		&cli.IntFlag{Name: "timeout"},
		&cli.IntFlag{Name: "retry"},
		&cli.DurationFlag{Name: "retry-delay"},
		&cli.BoolFlag{Name: "strict"},
	}
	for _, f := range flags {
		if err := f.Apply(set); err != nil {
			t.Fatalf("apply flag: %v", err)
		}
	}
	if err := set.Parse([]string{"--insecure"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	builder := &CLIBuilder{config: &Config{}}
	ctx := cli.NewContext(cli.NewApp(), set, nil)
	if err := builder.applyEnvConfig(ctx); err != nil {
		t.Fatalf("apply env config: %v", err)
	}
	if !builder.config.Insecure {
		t.Fatal("expected --insecure flag to set Config.Insecure")
	}
}

func TestHTTPBackedCommandsExposeInsecureFlag(t *testing.T) {
	builder := &CLIBuilder{config: &Config{}}
	commands := []*cli.Command{
		builder.GetQueryCommand(),
		builder.GetMutationCommand(),
		builder.GetSubscribeCommand(),
		builder.GetBatchCommand(),
		builder.GetTypesCommand(),
		builder.GetDescribeCommand(),
		builder.GetQueriesCommand(),
		builder.GetMutationsCommand(),
		builder.GetLoginCommand(),
	}

	for _, cmd := range commands {
		if !commandHasFlag(cmd, "insecure") {
			t.Fatalf("command %q does not expose --insecure", cmd.Name)
		}
	}
}

func commandHasFlag(cmd *cli.Command, name string) bool {
	for _, f := range cmd.Flags {
		for _, n := range f.Names() {
			if n == name {
				return true
			}
		}
	}
	return false
}
