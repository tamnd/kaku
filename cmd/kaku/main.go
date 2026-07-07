// kaku is a coding agent in one binary.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"github.com/tamnd/kaku/pkg/checkpoint"
	"github.com/tamnd/kaku/pkg/config"
	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/mcp"
	"github.com/tamnd/kaku/pkg/serve"
	"github.com/tamnd/kaku/pkg/session"
	"github.com/tamnd/kaku/pkg/tool/builtin"
	"github.com/tamnd/kaku/pkg/tui"
)

var version = "dev"

func main() {
	var o options
	var print bool
	var jsonOut bool

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
			if jsonOut {
				o.outputFormat = "json"
			}
			if print || jsonOut || prompt != "" && !isTTY(os.Stdout) {
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
					Model:       rt.agent.Model,
					Mode:        rt.cfg.Permissions.Mode,
					Dir:         rt.dir,
					MCPFailures: rt.mcpErrs,
					Models:      modelChoices(rt.cfg, rt.agent.Model),
					SwitchModel: rt.switchModel(o),
					Compact:     rt.compactor.Force,
				}, nil
			}})
		},
	}

	root.Flags().BoolVarP(&print, "print", "p", false, "run headless: answer the prompt and exit")
	root.Flags().BoolVar(&jsonOut, "json", false, "headless: emit one JSON event per line (sets --output-format json)")

	// Shared by the root run and the serve/mcp subcommands.
	fl := root.PersistentFlags()
	fl.StringVarP(&o.dir, "dir", "C", "", "work in this directory instead of the current one")
	fl.StringVar(&o.model, "model", "", "model reference: provider/model, bare model, or model:level")
	fl.StringVar(&o.provider, "provider", "", "provider: anthropic or openai")
	fl.StringVar(&o.baseURL, "base-url", "", "API base URL (local servers, proxies)")
	fl.StringVar(&o.apiKeyEnv, "api-key-env", "", "environment variable holding the API key")
	fl.StringVar(&o.thinking, "thinking", "", "reasoning level: off, minimal, low, medium, high, xhigh")
	fl.BoolVar(&o.hideThinking, "hide-thinking", false, "do not print thinking, even when reasoning is on")
	fl.StringVar(&o.mode, "mode", "", "permission mode: plan, ask, or auto")
	fl.BoolVarP(&o.resume, "continue", "c", false, "continue the newest session in this project")
	fl.BoolVar(&o.resume, "resume", false, "continue the newest session in this project (alias for --continue)")
	fl.BoolVar(&o.noSession, "no-session", false, "run without reading or writing a session file")
	fl.StringVar(&o.title, "title", "", "set the session title up front")
	fl.StringVar(&o.sessionID, "session", "", "continue a specific session id")
	fl.StringVar(&o.outputFormat, "output-format", "text", "headless output format: text or json")
	fl.IntVar(&o.maxTurns, "max-turns", 0, "cap on model turns per run")
	fl.BoolVar(&o.noMCP, "no-mcp", false, "skip connecting configured MCP servers")
	fl.BoolVar(&o.sandbox, "sandbox", false, "confine bash writes to the working directory (Seatbelt on macOS, landlock on Linux)")
	fl.StringVar(&o.tools, "tools", "", "allowlist of tools by name glob, comma separated (e.g. read,grep,glob,ls)")
	fl.StringVar(&o.excludeTools, "exclude-tools", "", "denylist of tools by name glob, comma separated")
	fl.BoolVar(&o.noTools, "no-tools", false, "run with no tools at all")
	fl.BoolVar(&o.noBuiltinTools, "no-builtin-tools", false, "drop the builtin tools but keep MCP and the agent tool")

	root.AddCommand(sessionsCmd(&o), modelsCmd(&o), rewindCmd(&o), serveCmd(&o), mcpCmd(&o), sandboxExecCmd())

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

	if o.outputFormat == "json" {
		return runPrintJSON(ctx, o, rt, prompt)
	}

	streamedText := false
	rt.agent.OnEvent = func(e engine.Event) {
		switch e.Type {
		case "text":
			streamedText = true
			fmt.Print(e.Text)
		case "thinking":
			if !o.hideThinking {
				fmt.Fprint(os.Stderr, dim(e.Text))
			}
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
	if o.title == "" && len(rt.sess.Messages()) == 0 {
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

// runPrintJSON is headless mode with JSONL output: a session header first, one
// event object per line, and a final result or error object. Nothing but the
// stream goes to stdout.
func runPrintJSON(ctx context.Context, o options, rt *runtime, prompt string) error {
	em := jsonEmitter{w: os.Stdout}
	em.session(rt.sess.ID(), rt.agent.Model, rt.dir)
	rt.agent.OnEvent = func(e engine.Event) {
		if e.Type == "thinking" && o.hideThinking {
			return
		}
		em.event(e)
	}

	input := rt.expandSkills(prompt)
	if o.title == "" && len(rt.sess.Messages()) == 0 {
		rt.sess.SetTitle(prompt)
	}
	out, err := rt.agent.Run(ctx, input)
	if err != nil {
		em.fail(err.Error())
		return err
	}
	em.result(out, rt.sess.Usage())
	return nil
}

// dim wraps text in the ANSI dim escape for muted stderr output.
func dim(s string) string { return "\x1b[2m" + s + "\x1b[0m" }

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

func modelsCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "models",
		Short: "List every model kaku can resolve",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := o.dir
			if dir == "" {
				var err error
				if dir, err = os.Getwd(); err != nil {
					return err
				}
			}
			cfg, err := config.Load(dir)
			if err != nil {
				return err
			}
			for _, m := range cfg.Models() {
				ref := m.Model
				if m.Provider != "" {
					ref = m.Provider + "/" + m.Model
				}
				reasoning := m.Reasoning
				if reasoning == "" {
					reasoning = "-"
				}
				line := fmt.Sprintf("%-40s  %s", ref, reasoning)
				if m.Default {
					line += "  (default)"
				}
				fmt.Println(line)
			}
			return nil
		},
	}
}

func serveCmd(o *options) *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the agent over HTTP with SSE streaming",
		Long: "serve exposes one agent conversation over HTTP.\n" +
			"POST /v1/messages streams engine events as SSE; GET /v1/history returns the conversation.\n" +
			"Like headless mode, ask-mode permission prompts degrade to deny; pass --mode auto to allow tools.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := build(cmd.Context(), *o)
			if err != nil {
				return err
			}
			defer rt.close()
			for name, err := range rt.mcpErrs {
				fmt.Fprintf(os.Stderr, "mcp %s: %v\n", name, err)
			}
			fmt.Fprintf(os.Stderr, "kaku serving %s on http://%s\n", rt.dir, addr)
			return serve.Run(cmd.Context(), addr, serve.Handler(rt.agent, rt.expandSkills))
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8377", "listen address")
	return cmd
}

func mcpCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Expose the agent as an MCP server on stdio",
		Long: "mcp turns kaku into an MCP server: add `kaku mcp` to another agent's\n" +
			"MCP config and it gains a `kaku` tool that runs prompts through this\n" +
			"project's agent. Calls share one conversation, so follow-ups keep context.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := build(cmd.Context(), *o)
			if err != nil {
				return err
			}
			defer rt.close()
			tools := []mcp.ServerTool{{
				Name:        "kaku",
				Description: "Run the kaku coding agent on a task in " + rt.dir + ". Calls share one conversation, so follow-up calls keep context.",
				Schema:      json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string","description":"The task or question for the agent"}},"required":["prompt"]}`),
				Run: func(ctx context.Context, args json.RawMessage) (string, error) {
					var in struct {
						Prompt string `json:"prompt"`
					}
					if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Prompt) == "" {
						return "", fmt.Errorf("prompt is required")
					}
					return rt.agent.Run(ctx, rt.expandSkills(in.Prompt))
				},
			}}
			return mcp.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), version, tools)
		},
	}
}

// sandboxExecCmd is the hidden Linux shim: --sandbox re-execs kaku through
// it so landlock is applied inside the child before bash starts.
func sandboxExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "sandbox-exec <workdir> <command>",
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return builtin.SandboxExec(args[0], args[1])
		},
	}
}

func rewindCmd(o *options) *cobra.Command {
	var list bool
	cmd := &cobra.Command{
		Use:   "rewind [checkpoint]",
		Short: "Restore the working tree to a checkpoint",
		Long: "Kaku snapshots the working tree before each turn that changes files.\n" +
			"rewind restores the newest snapshot, or the one you name.\n" +
			"The state before rewinding is snapshotted too, so a rewind can be undone.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := o.dir
			if dir == "" {
				var err error
				if dir, err = os.Getwd(); err != nil {
					return err
				}
			}
			cm, err := checkpoint.New(dir)
			if err != nil {
				return err
			}
			if list {
				infos, err := cm.List()
				if err != nil {
					return err
				}
				if len(infos) == 0 {
					fmt.Println("no checkpoints yet")
					return nil
				}
				for _, in := range infos {
					fmt.Println(in)
				}
				return nil
			}
			sha := ""
			if len(args) == 1 {
				sha = args[0]
			} else {
				latest, err := cm.Latest()
				if err != nil {
					return err
				}
				sha = latest.SHA
			}
			if err := cm.Restore(sha); err != nil {
				return err
			}
			fmt.Printf("restored %s\n", sha[:min(10, len(sha))])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&list, "list", "l", false, "list checkpoints instead of restoring")
	return cmd
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

// modelChoices turns the resolvable models into picker rows. The flat default
// is dropped when a named provider already serves the same model id, so the
// list never shows one model twice. current marks the active row.
func modelChoices(cfg *config.Config, current string) []tui.ModelChoice {
	infos := cfg.Models()
	named := map[string]bool{}
	for _, mi := range infos {
		if !mi.Default {
			named[mi.Model] = true
		}
	}
	var out []tui.ModelChoice
	for _, mi := range infos {
		var ref, label string
		switch {
		case mi.Default:
			if named[mi.Model] {
				continue
			}
			ref, label = mi.Model, mi.Model+"  (default)"
		default:
			ref = mi.Provider + "/" + mi.Model
			label = ref
		}
		out = append(out, tui.ModelChoice{
			Ref:       ref,
			Label:     label,
			Reasoning: mi.Reasoning,
			Current:   mi.Model == current,
		})
	}
	return out
}
