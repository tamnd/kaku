package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/tamnd/kaku/pkg/auth"
)

// authCmd groups the credential-store subcommands. Keys live in
// ~/.kaku/auth.json (0600) and fill in for a provider whose env var is unset.
func authCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage stored provider API keys",
		Long: "auth stores one API key per provider in ~/.kaku/auth.json (0600).\n" +
			"A stored key is used when the provider's environment variable is unset,\n" +
			"so you can log in once instead of exporting a key each session.",
	}
	cmd.AddCommand(authLoginCmd(), authListCmd(), authLogoutCmd())
	return cmd
}

func authLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login [provider]",
		Short: "Store an API key for a provider",
		Long: "login reads a key from the terminal without echoing it and stores it\n" +
			"under the provider name (anthropic, openai, or a named provider id).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := "anthropic"
			if len(args) == 1 {
				provider = strings.TrimSpace(args[0])
			}
			if provider == "" {
				return fmt.Errorf("provider is required")
			}
			key, err := readSecret(cmd, fmt.Sprintf("API key for %s: ", provider))
			if err != nil {
				return err
			}
			key = strings.TrimSpace(key)
			if key == "" {
				return fmt.Errorf("no key entered")
			}
			store, err := auth.New()
			if err != nil {
				return err
			}
			if err := store.Set(provider, key); err != nil {
				return err
			}
			fmt.Printf("stored key for %s\n", provider)
			return nil
		},
	}
}

func authListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List providers with a stored key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := auth.New()
			if err != nil {
				return err
			}
			names := store.List()
			if len(names) == 0 {
				fmt.Println("no stored keys")
				return nil
			}
			for _, n := range names {
				fmt.Println(n)
			}
			return nil
		},
	}
}

func authLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout [provider]",
		Short: "Remove a provider's stored key",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := "anthropic"
			if len(args) == 1 {
				provider = strings.TrimSpace(args[0])
			}
			if provider == "" {
				return fmt.Errorf("provider is required")
			}
			store, err := auth.New()
			if err != nil {
				return err
			}
			if err := store.Delete(provider); err != nil {
				return err
			}
			fmt.Printf("removed key for %s\n", provider)
			return nil
		},
	}
}

// readSecret prompts and reads one line. On a TTY the input is not echoed; when
// stdin is piped it reads a plain line so scripts can feed a key in.
func readSecret(cmd *cobra.Command, prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(f.Fd()) {
		b, err := term.ReadPassword(f.Fd())
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	r := bufio.NewReader(cmd.InOrStdin())
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}
