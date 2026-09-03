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

	flagsMap := make(map[string][]string)
	var positionals []string

	for i := 0; i < len(rawArgs); i++ {
		arg := rawArgs[i]

		// Everything after "--" is positional, even if it starts with "-"
		if arg == "--" {
			positionals = append(positionals, rawArgs[i+1:]...)
			break
		}

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
			if _, ok := tagToFieldIndex[key]; !ok {
				return xerr.BadRequest(fmt.Sprintf("unknown flag: -%s", key))
			}

			switch {
			case valStr != "":
				flagsMap[key] = append(flagsMap[key], valStr)
			case boolFields[key] && i+1 < len(rawArgs) && (rawArgs[i+1] == "true" || rawArgs[i+1] == "false"):
				flagsMap[key] = append(flagsMap[key], rawArgs[i+1])
				i++
			case boolFields[key]:
				flagsMap[key] = append(flagsMap[key], "true")
			case i+1 < len(rawArgs) && (!strings.HasPrefix(rawArgs[i+1], "-") || isNegativeNumber(rawArgs[i+1])):
				flagsMap[key] = append(flagsMap[key], rawArgs[i+1])
				i++
			default:
				return xerr.BadRequest(fmt.Sprintf("flag -%s requires a value", key))
			}
		}
	}

	for tagKey, fieldIdx := range tagToFieldIndex {
		if valList, ok := flagsMap[tagKey]; ok && len(valList) > 0 {
			fieldVal := elem.Field(fieldIdx)
			if fieldVal.Kind() == reflect.Slice && fieldVal.Type().Elem().Kind() == reflect.String {
				var items []string
				for _, v := range valList {
					for _, part := range strings.Split(v, ",") {
						if part = strings.TrimSpace(part); part != "" {
							items = append(items, part)
						}
					}
				}
				fieldVal.Set(reflect.ValueOf(items))
			} else {
				if err := setFieldValue(fieldVal, valList[len(valList)-1]); err != nil {
					return fmt.Errorf("flag -%s: %w", tagKey, err)
				}
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

func slicesContains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
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
