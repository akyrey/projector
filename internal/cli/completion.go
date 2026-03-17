package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newCompletionCmd returns a completion command that wraps cobra's built-in
// completion generator and adds install instructions for each shell.
func newCompletionCmd(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for projector.

To load completions in your current shell session:

  Bash:
    source <(projector completion bash)

  Zsh:
    source <(projector completion zsh)
    # If you get "command not found: compdef", add the following to ~/.zshrc first:
    #   autoload -Uz compinit && compinit

  Fish:
    projector completion fish | source

  PowerShell:
    projector completion powershell | Out-String | Invoke-Expression

To load completions permanently, add the source line above to your shell's
rc file (~/.bashrc, ~/.zshrc, ~/.config/fish/config.fish, etc.).`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
			default:
				return fmt.Errorf("unsupported shell: %q", args[0])
			}
		},
	}

	return cmd
}
