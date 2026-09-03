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

	typ := val.Type()

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		cliTag := field.Tag.Get("cli")

		if !strings.Contains(cliTag, "positional") &&
			!strings.Contains(cliTag, "args") {
			continue
		}

		label := strings.ToLower(field.Name)

		switch field.Type.Kind() {
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

	typ := val.Type()
	var flags []string

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		cliTag := field.Tag.Get("cli")
		usageTag := field.Tag.Get("usage")

		if cliTag == "" ||
			strings.Contains(cliTag, "positional") ||
			strings.Contains(cliTag, "args") {
			continue
		}

		parts := strings.Split(cliTag, ",")

		var formattedParts []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}

			if len(p) == 1 {
				formattedParts = append(formattedParts, "-"+p)
			} else {
				formattedParts = append(formattedParts, "--"+p)
			}
		}

		flagFormatted := strings.Join(formattedParts, ", ")

		typeHint := ""

		if field.Type == reflect.TypeOf(time.Time{}) {
			typeHint = " <time>"
		} else {
			switch field.Type.Kind() {
			case reflect.String:
				typeHint = " <string>"
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				typeHint = " <int>"
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				typeHint = " <uint>"
			case reflect.Float32, reflect.Float64:
				typeHint = " <float>"
			case reflect.Slice:
				typeHint = " <paths...>"
			case reflect.Bool:
				typeHint = ""
			}
		}

		if usageTag != "" {
			flags = append(flags, fmt.Sprintf("  %-32s %s", flagFormatted+typeHint, usageTag))
		} else {
			flags = append(flags, fmt.Sprintf("  %-32s", flagFormatted+typeHint))
		}
	}

	if len(flags) > 0 {
		fmt.Fprintf(t.stderr, "\nFlags:\n%s\n", strings.Join(flags, "\n"))
	}
}
