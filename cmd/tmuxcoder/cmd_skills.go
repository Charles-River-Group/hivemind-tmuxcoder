package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Skills commands",
}

var skillsInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install skills into Codex/Claude Code",
	Run: func(cmd *cobra.Command, args []string) {
		skillsDir, _ := cmd.Flags().GetString("skills-dir")
		target, _ := cmd.Flags().GetString("target")
		codexDir, _ := cmd.Flags().GetString("codex-dir")
		claudeDir, _ := cmd.Flags().GetString("claude-dir")
		force, _ := cmd.Flags().GetBool("force")

		var targets []string
		switch target {
		case "codex":
			targets = []string{expandHome(codexDir)}
		case "claude":
			targets = []string{expandHome(claudeDir)}
		case "both":
			targets = []string{expandHome(codexDir), expandHome(claudeDir)}
		default:
			fmt.Printf("Invalid --target: %s (expected codex|claude|both)\n", target)
			os.Exit(1)
		}

		if skillsDir == "" {
			skillNames, err := listSkillDirsFS(embeddedSkills, "skills")
			if err != nil {
				fmt.Printf("Failed to read embedded skills: %v\n", err)
				os.Exit(1)
			}
			if len(skillNames) == 0 {
				fmt.Println("No embedded skills found")
				os.Exit(1)
			}
			for _, dst := range targets {
				if err := installSkillsFromFS(embeddedSkills, "skills", dst, skillNames, force); err != nil {
					fmt.Printf("Install failed: %v\n", err)
					os.Exit(1)
				}
			}
		} else {
			srcDir := expandHome(skillsDir)
			if _, err := os.Stat(srcDir); err != nil {
				fmt.Printf("Skills dir not found: %s\n", srcDir)
				os.Exit(1)
			}
			skillNames, err := listSkillDirs(srcDir)
			if err != nil {
				fmt.Printf("Failed to read skills dir: %v\n", err)
				os.Exit(1)
			}
			if len(skillNames) == 0 {
				fmt.Printf("No skills found in: %s\n", srcDir)
				os.Exit(1)
			}
			for _, dst := range targets {
				if err := installSkills(srcDir, dst, skillNames, force); err != nil {
					fmt.Printf("Install failed: %v\n", err)
					os.Exit(1)
				}
			}
		}

		fmt.Println("Skills installed successfully.")
	},
}

func init() {
	skillsInstallCmd.Flags().String("skills-dir", "", "Path to skills directory (empty = use embedded)")
	skillsInstallCmd.Flags().String("target", "both", "Install target: codex|claude|both")
	skillsInstallCmd.Flags().String("codex-dir", "~/.codex/skills", "Codex skills directory")
	skillsInstallCmd.Flags().String("claude-dir", "~/.claude/skills", "Claude Code skills directory")
	skillsInstallCmd.Flags().Bool("force", false, "Overwrite existing skills")

	skillsCmd.AddCommand(skillsInstallCmd)
	rootCmd.AddCommand(skillsCmd)
}
