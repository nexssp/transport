package tcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"

	"github.com/nexssp/transport"
	"github.com/nexssp/transport/codec"
)

var _ transport.Transport = (*Transport)(nil)

type Option func(*Transport)

type Transport struct {
	actions    []action.AnyAction
	args       []string
	codec      codec.Codec
	stdout     io.Writer
	stderr     io.Writer
	executable string
}

func New(opts ...Option) *Transport {
	execName := "app"
	if len(os.Args) > 0 && os.Args[0] != "" {
		execName = filepath.Base(os.Args[0])
	}

	t := &Transport{
		args:       os.Args[1:],
		codec:      codec.Default,
		stdout:     os.Stdout,
		stderr:     os.Stderr,
		executable: execName,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}

	// Defensive nil-guards for public API safety
	if t.codec == nil {
		t.codec = codec.Default
	}
	if t.stdout == nil {
		t.stdout = io.Discard
	}
	if t.stderr == nil {
		t.stderr = io.Discard
	}

	return t
}

func WithArgs(args ...string) Option {
	return func(t *Transport) { t.args = args }
}

func WithCodec(c codec.Codec) Option {
	return func(t *Transport) {
		if c != nil {
			t.codec = c
		}
	}
}

func WithOutput(stdout, stderr io.Writer) Option {
	return func(t *Transport) {
		t.stdout = stdout
		t.stderr = stderr
	}
}

func (t *Transport) CanHandle(b action.Binding) bool {
	_, ok := b.(CLIBinding)
	return ok
}

func (t *Transport) String() string { return "cli" }

func (t *Transport) Mount(actions []action.AnyAction) {
	t.actions = actions
}

func (t *Transport) Do(ctx context.Context, _ any) (any, error) {
	if err := t.validateCommandBindings(); err != nil {
		fmt.Fprintf(t.stderr, "Error: %v\n", err)
		return nil, err
	}
	if len(t.args) == 0 {
		t.PrintHelp()
		return nil, nil //nolint:nilnil // one-shot CLI: help shown, no structured result to return
	}

	cmdArg := t.args[0]
	if cmdArg == "--help" || cmdArg == "-h" || cmdArg == "help" {
		return t.handleHelpCommand()
	}

	targetAction, err := t.findAction(cmdArg)
	if err != nil {
		fmt.Fprintf(t.stderr, "Error: %v\n", err)
		return nil, err
	}
	if targetAction == nil {
		fmt.Fprintf(t.stderr, "Error: unknown command %q\n\n", cmdArg)
		t.PrintHelp()
		return nil, xerr.NotFound(fmt.Sprintf("command %q not found", cmdArg))
	}

	// Intercept sub-command help (e.g. `srcpack pack --help`, `srcpack arch -h`)
	if hasHelpFlag(t.args[1:]) {
		t.PrintCommandHelp(targetAction)
		return nil, nil //nolint:nilnil // one-shot CLI: help shown, no structured result to return
	}

	return t.executeAction(ctx, targetAction)
}

func (t *Transport) validateCommandBindings() error {
	type owner struct {
		name  string
		index int
	}

	owners := make(map[string]owner)
	for index, act := range t.actions {
		if act == nil || act.Describe() == nil {
			continue
		}
		for _, binding := range act.GetBindings() {
			cliBind, ok := binding.(CLIBinding)
			if !ok {
				continue
			}
			for _, cmd := range append([]string{cliBind.Command}, cliBind.Aliases...) {
				if cmd == "" {
					continue
				}
				if previous, exists := owners[cmd]; exists && previous.index != index {
					return xerr.Conflict(fmt.Sprintf("tcli: command %q is already bound to %q and also bound to %q", cmd, previous.name, act.Describe().Name))
				}
				owners[cmd] = owner{name: act.Describe().Name, index: index}
			}
		}
	}
	return nil
}

// handleHelpCommand implements `app help [command]`.
func (t *Transport) handleHelpCommand() (any, error) {
	if len(t.args) <= 1 {
		t.PrintHelp()
		return nil, nil //nolint:nilnil // one-shot CLI: help shown, no structured result to return
	}

	target := t.args[1]
	act, err := t.findAction(target)
	if err != nil {
		fmt.Fprintf(t.stderr, "Error: %v\n", err)
		return nil, err
	}
	if act != nil {
		t.PrintCommandHelp(act)
		return nil, nil //nolint:nilnil // one-shot CLI: help shown, no structured result to return
	}

	fmt.Fprintf(t.stderr, "Error: unknown command %q\n\n", target)
	t.PrintHelp()
	return nil, xerr.NotFound(fmt.Sprintf("command %q not found", target))
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" || arg == "help" {
			return true
		}
	}
	return false
}

// executeAction resolves, decodes, and runs the target action, then renders its result.
func (t *Transport) executeAction(ctx context.Context, targetAction action.AnyAction) (any, error) {
	ex, ok := targetAction.(action.Executable)
	if !ok {
		return nil, xerr.Internal("action is not executable")
	}

	requestSchema, hasRequestSchema := requestSchemaFromAction(targetAction)
	decoder := func(target any) error {
		if hasRequestSchema {
			return bindCLIRequestTarget(target, t.args[1:], requestSchema)
		}
		return bindCLITarget(target, t.args[1:])
	}

	res, err := ex.ExecuteDecoded(ctx, decoder)
	if err != nil {
		fmt.Fprintf(t.stderr, "Error: %s\n", xerr.Sprint(err))
		return nil, err
	}

	if renderErr := t.renderResult(res); renderErr != nil {
		fmt.Fprintf(t.stderr, "Error: %s\n", xerr.Sprint(renderErr))
		return nil, renderErr
	}

	return res, nil
}

// renderResult prints the action result to stdout using the transport's codec.
func (t *Transport) renderResult(res any) error {
	if res == nil {
		return nil
	}

	switch out := res.(type) {
	case string:
		fmt.Fprintln(t.stdout, out)
		return nil
	case action.MessageRes:
		fmt.Fprintln(t.stdout, out.Message)
		return nil
	}

	var (
		data []byte
		err  error
	)

	if t.codec.Name() == "json" {
		data, err = json.MarshalIndent(res, "", "  ")
	} else {
		data, err = t.codec.Marshal(res)
	}
	if err != nil {
		return xerr.Internal("failed to format result", err)
	}

	fmt.Fprintln(t.stdout, string(data))
	return nil
}

func (t *Transport) findAction(cmd string) (action.AnyAction, error) {
	var found action.AnyAction
	for _, act := range t.actions {
		for _, b := range act.GetBindings() {
			if cliBind, ok := b.(CLIBinding); ok {
				if cliBind.Command == cmd || slices.Contains(cliBind.Aliases, cmd) {
					if found != nil && found.Describe().Name != act.Describe().Name {
						return nil, xerr.Conflict(fmt.Sprintf("tcli: command %q is already bound to %q and also bound to %q", cmd, found.Describe().Name, act.Describe().Name))
					}
					found = act
					break
				}
			}
		}
	}
	return found, nil
}

// WithExecutable overrides the binary name used in CLI help & usage output.
func WithExecutable(name string) Option {
	return func(t *Transport) {
		if name != "" {
			t.executable = name
		}
	}
}
