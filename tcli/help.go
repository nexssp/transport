package tcli

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/nexssp/kernel/action"
)

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

func (t *Transport) PrintCommandHelp(act action.AnyAction) {
	meta := act.Describe()
	var binding *CLIBinding

	for _, b := range act.GetBindings() {
		if cb, ok := b.(CLIBinding); ok {
			binding = &cb
			break
		}
	}

	if binding == nil {
		return
	}

	desc := binding.Description
	if desc == "" && meta != nil {
		desc = meta.Description
	}

	fmt.Fprintf(t.stderr, "Command: %s\n", binding.Command)
	if desc != "" {
		fmt.Fprintf(t.stderr, "%s\n", desc)
	}

	if len(binding.Aliases) > 0 {
		fmt.Fprintf(t.stderr, "\nAliases:\n  %s\n", strings.Join(binding.Aliases, ", "))
	}

	// Detect positional arguments once before rendering the usage line.
	positionalText := ""
	var payload any

	if tp, ok := act.(action.TypedPayload); ok {
		payload = tp.ReqPayload()
		positionalText = t.positionalUsage(payload)
	}

	fmt.Fprintf(
		t.stderr,
		"\nUsage:\n  %s %s [flags]%s\n",
		t.executable,
		binding.Command,
		positionalText,
	)

	if len(binding.Examples) > 0 {
		fmt.Fprintf(t.stderr, "\nExamples:\n")
		for _, ex := range binding.Examples {
			fmt.Fprintf(t.stderr, "  %s\n", ex)
		}
	}

	if payload != nil {
		t.printStructFlags(payload)
	}
}

func (t *Transport) positionalUsage(payload any) string {
	if payload == nil {
		return ""
	}

	val := reflect.ValueOf(payload)
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return ""
		}
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return ""
	}

	for field := range val.Type().Fields() {
		cliTag := field.Tag.Get("cli")
		if !strings.Contains(cliTag, "positional") && !strings.Contains(cliTag, "args") {
			continue
		}

		label := strings.ToLower(field.Name)

		switch field.Type.Kind() { //nolint:exhaustive // only Slice and String produce positional usage
		case reflect.Slice:
			if field.Type.Elem().Kind() == reflect.String {
				return " [" + label + "...]"
			}
		case reflect.String:
			return " [" + label + "]"
		}
	}

	return ""
}

func (t *Transport) printStructFlags(payload any) {
	if payload == nil {
		return
	}

	val := reflect.ValueOf(payload)
	if val.Kind() == reflect.Pointer {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return
	}

	flags := collectFlags(val.Type())
	if len(flags) > 0 {
		fmt.Fprintf(t.stderr, "\nFlags:\n%s\n", strings.Join(flags, "\n"))
	}
}

func collectFlags(typ reflect.Type) []string {
	var flags []string
	for field := range typ.Fields() {
		if !isFlagField(field) {
			continue
		}
		flags = append(flags, formatFlag(field))
	}
	return flags
}

func isFlagField(field reflect.StructField) bool {
	cliTag := field.Tag.Get("cli")
	return cliTag != "" &&
		!strings.Contains(cliTag, "positional") &&
		!strings.Contains(cliTag, "args")
}

func formatFlag(field reflect.StructField) string {
	names := formatFlagNames(field.Tag.Get("cli"))
	hint := flagTypeHint(field.Type)
	usage := field.Tag.Get("usage")

	if usage != "" {
		return fmt.Sprintf("  %-32s %s", names+hint, usage)
	}
	return fmt.Sprintf("  %-32s", names+hint)
}

func formatFlagNames(cliTag string) string {
	parts := strings.Split(cliTag, ",")
	formatted := make([]string, 0, len(parts))

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if len(p) == 1 {
			formatted = append(formatted, "-"+p)
		} else {
			formatted = append(formatted, "--"+p)
		}
	}
	return strings.Join(formatted, ", ")
}

func flagTypeHint(t reflect.Type) string {
	if t == reflect.TypeFor[time.Time]() {
		return " <time>"
	}

	switch t.Kind() { //nolint:exhaustive // only printable kinds are enumerated; others produce no type hint
	case reflect.String:
		return " <string>"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return " <int>"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return " <uint>"
	case reflect.Float32, reflect.Float64:
		return " <float>"
	case reflect.Slice:
		return " <paths...>"
	default:
		return ""
	}
}
