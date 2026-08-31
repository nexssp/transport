package tcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/transport"
	"github.com/nexssp/transport/codec"
)

type Option func(*Transport)

type Transport struct {
	actions []action.AnyAction
	args    []string
	codec   codec.Codec
	stdout  io.Writer
	stderr  io.Writer
}

var _ transport.Transport = (*Transport)(nil)

func New(opts ...Option) *Transport {
	t := &Transport{
		args:   os.Args[1:],
		codec:  codec.Default,
		stdout: os.Stdout,
		stderr: os.Stderr,
	}
	for _, opt := range opts {
		opt(t)
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
		t.PrintHelp()
		return nil, nil
	}

	var targetAction action.AnyAction
	for _, act := range t.actions {
		for _, b := range act.GetBindings() {
			if cliBind, ok := b.(CLIBinding); ok {
				if cliBind.Command == cmdArg || slicesContains(cliBind.Aliases, cmdArg) {
					targetAction = act
					break
				}
			}
		}
		if targetAction != nil {
			break
		}
	}

	if targetAction == nil {
		fmt.Fprintf(t.stderr, "Error: unknown command %q\n\n", cmdArg)
		t.PrintHelp()
		return nil, xerr.NotFound(fmt.Sprintf("command %q not found", cmdArg))
	}

	ex, ok := targetAction.(action.Executable)
	if !ok {
		return nil, xerr.Internal("action is not executable")
	}

	decoder := func(target any) error {
		return bindCLITarget(target, t.args[1:])
	}

	for _, arg := range t.args[1:] {
		if arg == "--help" || arg == "-h" || arg == "help" {
			t.PrintCommandHelp(targetAction)
			return nil, nil
		}
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

func (t *Transport) PrintHelp() {
	fmt.Fprintln(t.stderr, "Available Commands:")
	for _, act := range t.actions {
		meta := act.Describe()
		for _, b := range act.GetBindings() {
			if cliBind, ok := b.(CLIBinding); ok {
				desc := cliBind.Description
				if desc == "" && meta != nil {
					desc = meta.Description
				}
				fmt.Fprintf(t.stderr, "  %-18s %s\n", cliBind.Command, desc)
			}
		}
	}
}

func slicesContains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

func bindCLITarget(v any, rawArgs []string) error {
	val := reflect.ValueOf(v)
	if val.Kind() != reflect.Pointer || val.IsNil() {
		return errors.New("cli: target must be a non-nil pointer")
	}
	elem := val.Elem()
	if elem.Kind() != reflect.Struct {
		if elem.Kind() == reflect.String && len(rawArgs) > 0 {
			elem.SetString(strings.Join(rawArgs, " "))
		}
		return nil
	}

	typ := elem.Type()
	boolFields := make(map[string]bool)
	tagToFieldIndex := make(map[string]int)

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		cliTag := field.Tag.Get("cli")
		jsonTag := strings.Split(field.Tag.Get("json"), ",")[0]

		var keys []string
		if cliTag != "" {
			for _, k := range strings.Split(cliTag, ",") {
				k = strings.TrimSpace(k)
				if k != "" && k != "positional" && k != "args" {
					keys = append(keys, k)
				}
			}
		}
		if jsonTag != "" && jsonTag != "-" {
			keys = append(keys, jsonTag)
		}
		keys = append(keys, strings.ToLower(field.Name))

		for _, tagKey := range keys {
			tagToFieldIndex[tagKey] = i
			if field.Type.Kind() == reflect.Bool {
				boolFields[tagKey] = true
			}
		}
	}

	flagsMap := make(map[string]string)
	var positionals []string

	for i := 0; i < len(rawArgs); i++ {
		arg := rawArgs[i]
		var key, valStr string
		var isFlag bool

		switch {
		case strings.HasPrefix(arg, "--"):
			isFlag = true
			kv := strings.SplitN(arg[2:], "=", 2)
			key = kv[0]
			if len(kv) == 2 {
				valStr = kv[1]
			}
		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			isFlag = true
			kv := strings.SplitN(arg[1:], "=", 2)
			key = kv[0]
			if len(kv) == 2 {
				valStr = kv[1]
			}
		default:
			positionals = append(positionals, arg)
		}

		if isFlag {
			switch {
			case valStr != "":
				flagsMap[key] = valStr
			case boolFields[key] && i+1 < len(rawArgs) && (rawArgs[i+1] == "true" || rawArgs[i+1] == "false"):
				flagsMap[key] = rawArgs[i+1]
				i++
			case boolFields[key]:
				flagsMap[key] = "true"
			case i+1 < len(rawArgs) && !strings.HasPrefix(rawArgs[i+1], "-"):
				flagsMap[key] = rawArgs[i+1]
				i++
			default:
				flagsMap[key] = "true"
			}
		}
	}

	for tagKey, fieldIdx := range tagToFieldIndex {
		if strVal, ok := flagsMap[tagKey]; ok {
			if err := setFieldValue(elem.Field(fieldIdx), strVal); err != nil {
				return fmt.Errorf("flag -%s: %w", tagKey, err)
			}
		}
	}

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		cliTag := field.Tag.Get("cli")
		if strings.Contains(cliTag, "positional") || strings.Contains(cliTag, "args") {
			if field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.String {
				elem.Field(i).Set(reflect.ValueOf(positionals))
			} else if field.Type.Kind() == reflect.String && len(positionals) > 0 {
				elem.Field(i).SetString(strings.Join(positionals, " "))
			}
		}
	}

	return nil
}

func setFieldValue(f reflect.Value, val string) error {
	if f.Type() == reflect.TypeOf(time.Time{}) {
		layouts := []string{time.RFC3339, time.RFC3339Nano, time.DateOnly, time.DateTime}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, val); err == nil {
				f.Set(reflect.ValueOf(t))
				return nil
			}
		}
		return fmt.Errorf("invalid time format %q (expected RFC3339 or YYYY-MM-DD)", val)
	}

	switch f.Kind() {
	case reflect.String:
		f.SetString(val)
	case reflect.Bool:
		b, err := strconv.ParseBool(val)
		if err != nil {
			return err
		}
		f.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return err
		}
		f.SetInt(i)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u, err := strconv.ParseUint(val, 10, 64)
		if err != nil {
			return err
		}
		f.SetUint(u)
	case reflect.Float32, reflect.Float64:
		fl, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return err
		}
		f.SetFloat(fl)
	case reflect.Slice:
		if f.Type().Elem().Kind() == reflect.String {
			items := strings.Split(val, ",")
			f.Set(reflect.ValueOf(items))
		}
	}
	return nil
}

func (t *Transport) PrintCommandHelp(act action.AnyAction) {
	meta := act.Describe()
	for _, b := range act.GetBindings() {
		if cliBind, ok := b.(CLIBinding); ok {
			if cliBind.Command == t.args[0] || slicesContains(cliBind.Aliases, t.args[0]) {
				desc := cliBind.Description
				if desc == "" && meta != nil {
					desc = meta.Description
				}
				if desc != "" {
					fmt.Fprintf(t.stderr, "Usage: %s\n\n%s\n", cliBind.Command, desc)
				} else {
					fmt.Fprintf(t.stderr, "Usage: %s\n", cliBind.Command)
				}
				return
			}
		}
	}
}
