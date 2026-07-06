// kaku is a coding agent in one binary.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/session"
	"github.com/tamnd/kaku/pkg/tui"
)

var version = "dev"

func main() {
	var o options
	var print bool

	root := &cobra.Command{
		Use:   "kaku [prompt]",
		Short: "A coding agent that lives in your terminal",
		Long: "Kaku (書く, \"to write\") is a coding agent in one binary.\n" +
			"Run it bare for the interactive TUI, or with -p for a single headless run.",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.TrimSpace(strings.Join(args, " "))
			stdin := cmd.InOrStdin()
			if f, ok := stdin.(*os.File); ok && !isTTY(f) {
				data, err := io.ReadAll(stdin)
				if err != nil {
					return err
				}
				if piped := strings.TrimSpace(string(data)); piped != "" {
					if prompt != "" {
						prompt = prompt + "\n\n" + piped
					} else {
						prompt = piped
					}
					print = true
				}
			}
			if print || prompt != "" && !isTTY(os.Stdout) {
				if prompt == "" {
					return fmt.Errorf("print mode needs a prompt: kaku -p \"do the thing\"")
				}
				return runPrint(cmd.Context(), o, prompt)
			}
			if prompt != "" {
				return runPrint(cmd.Context(), o, prompt)
			}
			return tui.Run(cmd.Context(), tui.Options{Build: func(ctx context.Context) (tui.Runtime, error) {
				rt, err := build(ctx, o)
				if err != nil {
					return tui.Runtime{}, err
				}
				return tui.Runtime{
					Agent:       rt.agent,
					Session:     rt.sess,
					Skills:      rt.skills,
					Expand:      rt.expandSkills,
					Close:       rt.close,
					Model:       rt.cfg.Model,
					Mode:        rt.cfg.Permissions.Mode,
					Dir:         rt.dir,
					MCPFailures: rt.mcpErrs,
				}, nil
			}})
		},
	}

	fl := root.Flags()
	fl.BoolVarP(&print, "print", "p", false, "run headless: answer the prompt and exit")
	fl.StringVarP(&o.dir, "dir", "C", "", "work in this directory instead of the current one")
	fl.StringVar(&o.model, "model", "", "model override")
	fl.StringVar(&o.provider, "provider", "", "provider: anthropic or openai")
	fl.StringVar(&o.baseURL, "base-url", "", "API base URL (local servers, proxies)")
	fl.StringVar(&o.apiKeyEnv, "api-key-env", "", "environment variable holding the API key")
	fl.StringVar(&o.mode, "mode", "", "permission mode: plan, ask, or auto")
	fl.BoolVar(&o.resume, "resume", false, "continue the newest session in this project")
	fl.StringVar(&o.sessionID, "session", "", "continue a specific session id")
	fl.IntVar(&o.maxTurns, "max-turns", 0, "cap on model turns per run")
	fl.BoolVar(&o.noMCP, "no-mcp", false, "skip connecting configured MCP servers")

	root.AddCommand(sessionsCmd(&o))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := fang.Execute(ctx, root, fang.WithVersion(version)); err != nil {
		os.Exit(1)
	}
}

// runPrint is headless mode: stream the answer to stdout, tool activity to
// stderr, exit non-zero on failure.
func runPrint(ctx context.Context, o options, prompt string) error {
	// Headless runs cannot prompt, so ask mode degrades to deny unless the
	// user opted into auto.
	rt, err := build(ctx, o)
	if err != nil {
		return err
	}
	defer rt.close()
	for name, err := range rt.mcpErrs {
		fmt.Fprintf(os.Stderr, "mcp %s: %v\n", name, err)
	}

	streamedText := false
	rt.agent.OnEvent = func(e engine.Event) {
		switch e.Type {
		case "text":
			streamedText = true
			fmt.Print(e.Text)
		case "tool_start":
			fmt.Fprintf(os.Stderr, "· %s(%s)\n", e.Tool, oneLine(string(e.ToolInput), 120))
		case "tool_end":
			if e.IsError {
				fmt.Fprintf(os.Stderr, "  ! %s\n", oneLine(e.ToolOutput, 200))
			}
		case "info":
			fmt.Fprintf(os.Stderr, "%s\n", e.Text)
		}
	}

	input := rt.expandSkills(prompt)
	if len(rt.sess.Messages()) == 0 {
		rt.sess.SetTitle(prompt)
	}
	out, err := rt.agent.Run(ctx, input)
	if err != nil {
		return err
	}
	if !streamedText && out != "" {
		fmt.Print(out)
	}
	fmt.Println()
	return nil
}

func sessionsCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "List this project's sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := o.dir
			if dir == "" {
				var err error
				if dir, err = os.Getwd(); err != nil {
					return err
				}
			}
			metas, err := session.NewStore(dir).List()
			if err != nil {
				return err
			}
			if len(metas) == 0 {
				fmt.Println("no sessions yet")
				return nil
			}
			for _, m := range metas {
				fmt.Println(m.String())
			}
			return nil
		},
	}
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}
