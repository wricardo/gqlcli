package gqlcli

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v2"
)

//go:embed skill/SKILL.md
var skillMD []byte

// GetInstallSkillCommand returns the install-skill subcommand.
func (b *CLIBuilder) GetInstallSkillCommand() *cli.Command {
	return &cli.Command{
		Name:  "install-skill",
		Usage: "Install the gqlcli Claude Code skill to ~/.claude/skills/gqlcli/",
		Description: "Installs the gqlcli skill for Claude Code so that Claude knows how to use " +
			"gqlcli. Skips installation if the skill is already present.",
		Action: func(c *cli.Context) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("could not determine home directory: %w", err)
			}

			skillDir := filepath.Join(home, ".claude", "skills", "gqlcli")
			skillFile := filepath.Join(skillDir, "SKILL.md")

			if _, err := os.Stat(skillFile); err == nil {
				fmt.Println("skill already installed at", skillFile)
				return nil
			}

			if err := os.MkdirAll(skillDir, 0755); err != nil {
				return fmt.Errorf("could not create skill directory: %w", err)
			}

			if err := os.WriteFile(skillFile, skillMD, 0644); err != nil {
				return fmt.Errorf("could not write skill file: %w", err)
			}

			fmt.Println("skill installed at", skillFile)
			return nil
		},
	}
}
