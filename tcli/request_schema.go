package tcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
)

// CLI type names reused across parsing, coercion, and help rendering.
const (
	cliTypeString  = "string"
	cliTypeBool    = "bool"
	cliTypeInt8    = "int8"
	cliTypeInt16   = "int16"
	cliTypeInt32   = "int32"
	cliTypeUint8   = "uint8"
	cliTypeUint16  = "uint16"
	cliTypeUint32  = "uint32"
	cliTypeFloat32 = "float32"
)

func requestSchemaFromAction(act action.AnyAction) (CLIRequestSchema, bool) {
	if act == nil {
		return CLIRequestSchema{}, false
	}
	for _, binding := range act.GetBindings() {
		if schemaBinding, ok := binding.(CLIInputBinding); ok {
			return schemaBinding.Schema, true
		}
	}
	return CLIRequestSchema{}, false
}

// ValidateRequestSchema checks field names and flag aliases for ambiguity.
func ValidateRequestSchema(schema CLIRequestSchema) error {
	if strings.TrimSpace(schema.Name) == "" {
		return errors.New("cli: request schema name is required")
	}
	if len(schema.Fields) == 0 {
		return fmt.Errorf("cli: request schema %q has no fields", schema.Name)
	}

	fieldNames := make(map[string]bool, len(schema.Fields))
	flagOwners := make(map[string]int)
	for index, field := range schema.Fields {
		if strings.TrimSpace(field.Name) == "" {
			return fmt.Errorf("cli: request schema %q field %d has no payload name", schema.Name, index)
		}
		if fieldNames[field.Name] {
			return fmt.Errorf("cli: request schema %q has duplicate field %q", schema.Name, field.Name)
		}
		fieldNames[field.Name] = true
		if strings.TrimSpace(field.Type) == "" {
			return fmt.Errorf("cli: request schema %q field %q has no type", schema.Name, field.Name)
		}
		if field.Positional && len(field.Flags) > 0 {
			return fmt.Errorf("cli: request schema %q positional field %q cannot also declare flags", schema.Name, field.Name)
		}
		for _, alias := range requestFieldAliases(field) {
			alias = normalizeFlagAlias(alias)
			if alias == "" {
				continue
			}
			if previous, exists := flagOwners[alias]; exists && previous != index {
				return fmt.Errorf("cli: request schema %q flag %q is assigned to both %q and %q", schema.Name, alias, schema.Fields[previous].Name, field.Name)
			}
			flagOwners[alias] = index
		}
	}
	return nil
}

func requestFieldAliases(field CLIFieldSpec) []string {
	if field.Positional {
		return nil
	}
	aliases := append([]string(nil), field.Flags...)
	foundName := false
	for _, alias := range aliases {
		if normalizeFlagAlias(alias) == field.Name {
			foundName = true
			break
		}
	}
	if !foundName {
		aliases = append(aliases, field.Name)
	}
	return aliases
}

func normalizeFlagAlias(alias string) string {
	alias = strings.TrimSpace(alias)
	alias = strings.TrimLeft(alias, "-")
	return alias
}

func bindCLIRequestTarget(target any, rawArgs []string, schema CLIRequestSchema) error {
	if err := ValidateRequestSchema(schema); err != nil {
		return err
	}
	payload, err := parseCLIRequest(rawArgs, schema)
	if err != nil {
		return err
	}
	return action.Assign(target, payload)
}

// ParseCLIRequest parses raw CLI arguments against a request schema
// and returns a typed payload map.
func ParseCLIRequest(rawArgs []string, schema CLIRequestSchema) (map[string]any, error) {
	if err := ValidateRequestSchema(schema); err != nil {
		return nil, err
	}
	return parseCLIRequest(rawArgs, schema)
}

type cliSchemaIndex struct {
	fieldByFlag      map[string]int
	boolFlags        map[string]bool
	positionalFields []int
}

func indexCLISchema(schema CLIRequestSchema) cliSchemaIndex {
	idx := cliSchemaIndex{
		fieldByFlag: make(map[string]int),
		boolFlags:   make(map[string]bool),
	}
	for index, field := range schema.Fields {
		if field.Positional {
			idx.positionalFields = append(idx.positionalFields, index)
			continue
		}
		for _, alias := range requestFieldAliases(field) {
			alias = normalizeFlagAlias(alias)
			if alias == "" {
				continue
			}
			idx.fieldByFlag[alias] = index
			idx.boolFlags[alias] = isCLIType(field.Type, cliTypeBool)
		}
	}
	return idx
}

func parseCLIRequest(rawArgs []string, schema CLIRequestSchema) (map[string]any, error) {
	idx := indexCLISchema(schema)

	values := make([][]string, len(schema.Fields))
	present := make([]bool, len(schema.Fields))

	positionals, err := collectCLIValues(rawArgs, idx, values, present)
	if err != nil {
		return nil, err
	}

	if err := applyCLIPositionals(positionals, idx.positionalFields, schema, values, present); err != nil {
		return nil, err
	}

	return buildCLIPayload(schema, values, present)
}

func collectCLIValues(
	rawArgs []string,
	idx cliSchemaIndex,
	values [][]string,
	present []bool,
) (positionals []string, err error) {
	for i := 0; i < len(rawArgs); i++ {
		arg := rawArgs[i]
		if arg == "--" {
			positionals = append(positionals, rawArgs[i+1:]...)
			break
		}
		key, value, isFlag := parseFlag(arg)
		if !isFlag {
			positionals = append(positionals, arg)
			continue
		}
		index, exists := idx.fieldByFlag[key]
		if !exists {
			return nil, xerr.BadRequest("unknown flag: -" + key)
		}
		parsed, consumed, resolveErr := resolveFlagValue(rawArgs, i, key, value, idx.boolFlags)
		if resolveErr != nil {
			return nil, resolveErr
		}
		i += consumed
		values[index] = append(values[index], parsed)
		present[index] = true
	}
	return positionals, nil
}

func applyCLIPositionals(
	positionals []string,
	positionalFields []int,
	schema CLIRequestSchema,
	values [][]string,
	present []bool,
) error {
	positionalIndex := 0
	for _, raw := range positionals {
		if positionalIndex >= len(positionalFields) {
			return xerr.BadRequest(fmt.Sprintf("unexpected positional argument %q", raw))
		}
		fieldIndex := positionalFields[positionalIndex]
		values[fieldIndex] = append(values[fieldIndex], raw)
		present[fieldIndex] = true
		if !isCLISliceType(schema.Fields[fieldIndex].Type) {
			positionalIndex++
		}
	}
	return nil
}

func buildCLIPayload(schema CLIRequestSchema, values [][]string, present []bool) (map[string]any, error) {
	payload := make(map[string]any)
	for index, field := range schema.Fields {
		if !present[index] {
			if field.Required {
				return nil, xerr.BadRequest(fmt.Sprintf("missing required CLI field %q", field.Name))
			}
			continue
		}
		value, err := parseCLIFieldValues(field, values[index])
		if err != nil {
			return nil, fmt.Errorf("cli: field %q: %w", field.Name, err)
		}
		payload[field.Name] = value
	}
	return payload, nil
}

func isCLIType(raw, expected string) bool {
	typeName := strings.TrimSpace(raw)
	for strings.HasPrefix(typeName, "*") {
		typeName = strings.TrimSpace(typeName[1:])
	}
	return typeName == expected
}

func isCLISliceType(raw string) bool {
	typeName := strings.TrimSpace(raw)
	for strings.HasPrefix(typeName, "*") {
		typeName = strings.TrimSpace(typeName[1:])
	}
	return strings.HasPrefix(typeName, "[]")
}

func parseCLIFieldValues(field CLIFieldSpec, rawValues []string) (any, error) {
	typeName := strings.TrimSpace(field.Type)
	for strings.HasPrefix(typeName, "*") {
		typeName = strings.TrimSpace(typeName[1:])
	}
	if strings.HasPrefix(typeName, "[]") {
		elemType := strings.TrimSpace(typeName[2:])
		var elements []string
		for _, raw := range rawValues {
			for part := range strings.SplitSeq(raw, ",") {
				if part = strings.TrimSpace(part); part != "" {
					elements = append(elements, part)
				}
			}
		}
		if elemType == cliTypeString {
			return elements, nil
		}
		out := make([]any, 0, len(elements))
		for _, element := range elements {
			parsed, err := parseCLIValue(elemType, element)
			if err != nil {
				return nil, err
			}
			out = append(out, parsed)
		}
		return out, nil
	}
	if len(rawValues) == 0 {
		return nil, errors.New("no value provided")
	}
	return parseCLIValue(typeName, rawValues[len(rawValues)-1])
}

func parseCLIValue(typeName, raw string) (any, error) {
	switch typeName {
	case cliTypeString:
		return raw, nil
	case cliTypeBool:
		return strconv.ParseBool(raw)
	case "int", cliTypeInt8, cliTypeInt16, cliTypeInt32, "int64":
		bits := cliIntegerBits(typeName)
		return strconv.ParseInt(raw, 10, bits)
	case "uint", cliTypeUint8, cliTypeUint16, cliTypeUint32, "uint64":
		bits := cliIntegerBits(typeName)
		return strconv.ParseUint(raw, 10, bits)
	case cliTypeFloat32, "float64":
		bits := 64
		if typeName == cliTypeFloat32 {
			bits = 32
		}
		return strconv.ParseFloat(raw, bits)
	default:
		var decoded any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return nil, fmt.Errorf("type %s expects a JSON value: %w", typeName, err)
		}
		return decoded, nil
	}
}

func cliIntegerBits(typeName string) int {
	switch typeName {
	case cliTypeInt8, cliTypeUint8:
		return 8
	case cliTypeInt16, cliTypeUint16:
		return 16
	case cliTypeInt32, cliTypeUint32:
		return 32
	default:
		return 64
	}
}

func requestSchemaPositionalUsage(schema CLIRequestSchema) string {
	for _, field := range schema.Fields {
		if !field.Positional {
			continue
		}
		name := strings.ToLower(field.Name)
		if isCLISliceType(field.Type) {
			return " [" + name + "...]"
		}
		return " [" + name + "]"
	}
	return ""
}

func (t *Transport) printRequestSchemaFlags(schema CLIRequestSchema) {
	var flags []string
	for _, field := range schema.Fields {
		if field.Positional {
			continue
		}
		aliases := field.Flags
		if len(aliases) == 0 {
			aliases = []string{field.Name}
		}
		formatted := make([]string, 0, len(aliases))
		for _, alias := range aliases {
			alias = normalizeFlagAlias(alias)
			if alias == "" {
				continue
			}
			if len(alias) == 1 {
				formatted = append(formatted, "-"+alias)
			} else {
				formatted = append(formatted, "--"+alias)
			}
		}
		if len(formatted) == 0 {
			continue
		}
		label := strings.Join(formatted, ", ") + cliSchemaTypeHint(field.Type)
		if field.Required {
			label += " (required)"
		}
		if field.Usage != "" {
			flags = append(flags, fmt.Sprintf("  %-36s %s", label, field.Usage))
		} else {
			flags = append(flags, fmt.Sprintf("  %-36s", label))
		}
	}
	if len(flags) > 0 {
		fmt.Fprintf(t.stderr, "\nFlags:\n%s\n", strings.Join(flags, "\n"))
	}
}

func cliSchemaTypeHint(raw string) string {
	typeName := strings.TrimSpace(raw)
	for strings.HasPrefix(typeName, "*") {
		typeName = strings.TrimSpace(typeName[1:])
	}
	if strings.HasPrefix(typeName, "[]") {
		return " <values...>"
	}
	switch typeName {
	case cliTypeBool:
		return ""
	case cliTypeString:
		return " <string>"
	case "int", cliTypeInt8, cliTypeInt16, cliTypeInt32, "int64":
		return " <int>"
	case "uint", cliTypeUint8, cliTypeUint16, cliTypeUint32, "uint64":
		return " <uint>"
	case cliTypeFloat32, "float64":
		return " <float>"
	default:
		return " <json>"
	}
}
