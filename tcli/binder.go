package tcli

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/nexssp/kernel/xerr"
)

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
	tagToFieldIndex, boolFields := buildFieldIndex(typ)

	flagsMap, positionals, err := parseRawArgs(rawArgs, tagToFieldIndex, boolFields)
	if err != nil {
		return err
	}

	if err := applyFlags(elem, tagToFieldIndex, flagsMap); err != nil {
		return err
	}

	applyPositionals(elem, typ, positionals)
	return nil
}

// buildFieldIndex maps every CLI/json/lowercased-field-name key to its field index,
// and records which of those keys refer to boolean fields.
func buildFieldIndex(typ reflect.Type) (tagToFieldIndex map[string]int, boolFields map[string]bool) {
	tagToFieldIndex = make(map[string]int, typ.NumField()*3)
	boolFields = make(map[string]bool, 4)

	for i := range typ.NumField() {
		field := typ.Field(i)
		for _, key := range fieldKeys(field) {
			tagToFieldIndex[key] = i
			if field.Type.Kind() == reflect.Bool {
				boolFields[key] = true
			}
		}
	}
	return tagToFieldIndex, boolFields
}

// fieldKeys returns the set of names under which a struct field can be addressed.
func fieldKeys(field reflect.StructField) []string {
	var keys []string

	cliTag := field.Tag.Get("cli")
	if cliTag != "" {
		for k := range strings.SplitSeq(cliTag, ",") {
			k = strings.TrimSpace(k)
			if k != "" && k != "positional" && k != "args" {
				keys = append(keys, k)
			}
		}
	}

	jsonTag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	if jsonTag != "" && jsonTag != "-" {
		keys = append(keys, jsonTag)
	}

	keys = append(keys, strings.ToLower(field.Name))
	return keys
}

// parseRawArgs walks argv and separates flags from positional arguments.
func parseRawArgs(
	rawArgs []string,
	tagToFieldIndex map[string]int,
	boolFields map[string]bool,
) (flagsMap map[string][]string, positionals []string, err error) {
	flagsMap = make(map[string][]string)

	for i := 0; i < len(rawArgs); i++ {
		arg := rawArgs[i]

		if arg == "--" {
			positionals = append(positionals, rawArgs[i+1:]...)
			break
		}

		key, valStr, isFlag := parseFlag(arg)
		if !isFlag {
			positionals = append(positionals, arg)
			continue
		}

		if _, ok := tagToFieldIndex[key]; !ok {
			return nil, nil, xerr.BadRequest("unknown flag: -" + key)
		}

		value, consumed, err := resolveFlagValue(rawArgs, i, key, valStr, boolFields)
		if err != nil {
			return nil, nil, err
		}
		i += consumed
		flagsMap[key] = append(flagsMap[key], value)
	}
	return flagsMap, positionals, nil
}

// parseFlag splits one argv token into (key, value, isFlag).
func parseFlag(arg string) (key, valStr string, isFlag bool) {
	switch {
	case strings.HasPrefix(arg, "--"):
		kv := strings.SplitN(arg[2:], "=", 2)
		key = kv[0]
		if len(kv) == 2 {
			valStr = kv[1]
		}
		return key, valStr, true
	case strings.HasPrefix(arg, "-") && len(arg) > 1:
		kv := strings.SplitN(arg[1:], "=", 2)
		key = kv[0]
		if len(kv) == 2 {
			valStr = kv[1]
		}
		return key, valStr, true
	default:
		return "", "", false
	}
}

// resolveFlagValue returns the effective value for a flag and how many extra
// argv slots were consumed beyond the flag token itself.
func resolveFlagValue(
	rawArgs []string,
	i int,
	key, valStr string,
	boolFields map[string]bool,
) (value string, extraConsumed int, err error) {
	switch {
	case valStr != "":
		return valStr, 0, nil
	case boolFields[key] && i+1 < len(rawArgs) && (rawArgs[i+1] == "true" || rawArgs[i+1] == "false"):
		return rawArgs[i+1], 1, nil
	case boolFields[key]:
		return "true", 0, nil
	case i+1 < len(rawArgs) && (!strings.HasPrefix(rawArgs[i+1], "-") || isNegativeNumber(rawArgs[i+1])):
		return rawArgs[i+1], 1, nil
	default:
		return "", 0, xerr.BadRequest(fmt.Sprintf("flag -%s requires a value", key))
	}
}

// applyFlags writes parsed flag values into struct fields, accumulating slices
// and coercing scalars.
func applyFlags(elem reflect.Value, tagToFieldIndex map[string]int, flagsMap map[string][]string) error {
	for tagKey, fieldIdx := range tagToFieldIndex {
		valList, ok := flagsMap[tagKey]
		if !ok || len(valList) == 0 {
			continue
		}

		fieldVal := elem.Field(fieldIdx)

		if fieldVal.Kind() == reflect.Slice && fieldVal.Type().Elem().Kind() == reflect.String {
			fieldVal.Set(reflect.ValueOf(accumulateStrings(valList)))
			continue
		}

		if err := setFieldValue(fieldVal, valList[len(valList)-1]); err != nil {
			return fmt.Errorf("flag -%s: %w", tagKey, err)
		}
	}
	return nil
}

func accumulateStrings(valList []string) []string {
	var items []string
	for _, v := range valList {
		for part := range strings.SplitSeq(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				items = append(items, part)
			}
		}
	}
	return items
}

func applyPositionals(elem reflect.Value, typ reflect.Type, positionals []string) {
	for i := range typ.NumField() {
		field := typ.Field(i)
		cliTag := field.Tag.Get("cli")
		if !strings.Contains(cliTag, "positional") && !strings.Contains(cliTag, "args") {
			continue
		}

		switch {
		case field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.String:
			elem.Field(i).Set(reflect.ValueOf(positionals))
		case field.Type.Kind() == reflect.String && len(positionals) > 0:
			elem.Field(i).SetString(strings.Join(positionals, " "))
		}
	}
}

func setFieldValue(f reflect.Value, val string) error {
	if f.Type() == reflect.TypeFor[time.Time]() {
		layouts := []string{time.RFC3339, time.RFC3339Nano, time.DateOnly, time.DateTime}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, val); err == nil {
				f.Set(reflect.ValueOf(t))
				return nil
			}
		}
		return fmt.Errorf("invalid time format %q (expected RFC3339 or YYYY-MM-DD)", val)
	}

	switch f.Kind() { //nolint:exhaustive // default is a no-op for unsupported kinds
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

func isNegativeNumber(s string) bool {
	if len(s) < 2 || s[0] != '-' {
		return false
	}
	if s[1] == '.' {
		return true
	}
	return s[1] >= '0' && s[1] <= '9'
}
