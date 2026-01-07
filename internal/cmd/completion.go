package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// newCompletionCmd creates a completion command for generating shell completion scripts
func newCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate completion script",
		Long: `To load completions:

Bash:
  $ source <(awmg completion bash)

  # To load completions for each session, execute once:
  # Linux:
  $ awmg completion bash > /etc/bash_completion.d/awmg
  # macOS:
  $ awmg completion bash > $(brew --prefix)/etc/bash_completion.d/awmg

Zsh:
  # If shell completion is not already enabled in your environment,
  # you will need to enable it.  You can execute the following once:
  $ echo "autoload -U compinit; compinit" >> ~/.zshrc

  # To load completions for each session, execute once:
  $ awmg completion zsh > "${fpath[1]}/_awmg"

  # You will need to start a new shell for this setup to take effect.

Fish:
  $ awmg completion fish | source

  # To load completions for each session, execute once:
  $ awmg completion fish > ~/.config/fish/completions/awmg.fish

PowerShell:
  PS> awmg completion powershell | Out-String | Invoke-Expression

  # To load completions for every new session, run:
  PS> awmg completion powershell > awmg.ps1
  # and source this file from your PowerShell profile.
`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				// This default case should never be reached due to Args validation
				// above, but is included for defensive programming.
				return fmt.Errorf("unsupported shell type: %s", args[0])
			}
		},
	}
}
