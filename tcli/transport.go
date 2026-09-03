package tcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/transport"
	"github.com/nexssp/transport/codec"
)

type Option func(*Transport)

type Transport struct {
	actions    []action.AnyAction
	args       []string
	codec      codec.Codec
	stdout     io.Writer
	stderr     io.Writer
	executable string
}

var _ transport.Transport = (*Transport)(nil)

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
	if len(t.args) == 0 {
		t.PrintHelp()
		return nil, nil
	}

	cmdArg := t.args[0]
	if cmdArg == "--help" || cmdArg == "-h" || cmdArg == "help" {
		if len(t.args) > 1 {
			target := t.args[1]

			if act := t.findAction(target); act != nil {
				t.PrintCommandHelp(act)
				return nil, nil
			}

			fmt.Fprintf(t.stderr, "Error: unknown command %q\n\n", target)
			t.PrintHelp()
			return nil, xerr.NotFound(fmt.Sprintf("command %q not found", target))
		}

		t.PrintHelp()
		return nil, nil
	}

	targetAction := t.findAction(cmdArg)
	if targetAction == nil {
		fmt.Fprintf(t.stderr, "Error: unknown command %q\n\n", cmdArg)
		t.PrintHelp()
		return nil, xerr.NotFound(fmt.Sprintf("command %q not found", cmdArg))
	}

	// Intercept sub-command help (e.g. `srcpack pack --help`, `srcpack arch -h`)
	for _, arg := range t.args[1:] {
		if arg == "--help" || arg == "-h" || arg == "help" {
			t.PrintCommandHelp(targetAction)
			return nil, nil
		}
	}

	ex, ok := targetAction.(action.Executable)
	if !ok {
		return nil, xerr.Internal("action is not executable")
	}

	decoder := func(target any) error {
		return bindCLITarget(target, t.args[1:])
	}

	res, err := ex.ExecuteDecoded(ctx, decoder)
	if err != nil {
		fmt.Fprintf(t.stderr, "Error: %s\n", xerr.Sprint(err))
		return nil, err
	}

	if res != nil {
		switch out := res.(type) {
		case string:
			fmt.Fprintln(t.stdout, out)
		case action.MessageRes:
			fmt.Fprintln(t.stdout, out.Message)
		default:
			if t.codec.Name() == "json" {
				data, _ := json.MarshalIndent(res, "", "  ")
				fmt.Fprintln(t.stdout, string(data))
			} else {
				data, _ := t.codec.Marshal(res)
				fmt.Fprintln(t.stdout, string(data))
			}
		}
	}

	return res, nil
}

func (t *Transport) findAction(cmd string) action.AnyAction {
	for _, act := range t.actions {
		for _, b := range act.GetBindings() {
			if cliBind, ok := b.(CLIBinding); ok {
				if cliBind.Command == cmd || slicesContains(cliBind.Aliases, cmd) {
					return act
				}
			}
		}
	}
	return nil
}

// WithExecutable overrides the binary name used in CLI help & usage output.
func WithExecutable(name string) Option {
	return func(t *Transport) {
		if name != "" {
			t.executable = name
		}
	}
}
