package thttp

import (
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/nexssp/kernel/xerr"
)

type bindingPlan struct {
	fields []fieldBinding
}

type fieldBinding struct {
	idx       int
	extractor func(*http.Request) (reflect.Value, error)
}

var bindingCache sync.Map

// BindFromTags extracts parameters from path, query, header, cookie, and form struct tags.
func BindFromTags(v any, r *http.Request) error {
	val := reflect.ValueOf(v)
	if val.Kind() != reflect.Pointer || val.IsNil() {
		return xerr.Internal("bindFromTags: expected non-nil pointer to struct")
	}
	elem := val.Elem()
	if elem.Kind() != reflect.Struct {
		return nil
	}

	typ := elem.Type()
	planAny, ok := bindingCache.Load(typ)
	if !ok {
		p, err := buildBindingPlan(typ)
		if err != nil {
			return fmt.Errorf("build binding plan: %w", err)
		}
		planAny, _ = bindingCache.LoadOrStore(typ, p)
	}

	plan := planAny.(*bindingPlan)
	for _, fb := range plan.fields {
		fieldVal, err := fb.extractor(r)
		if err != nil {
			return err
		}
		if fieldVal.IsValid() {
			elem.Field(fb.idx).Set(fieldVal)
		}
	}
	return nil
}

func buildBindingPlan(typ reflect.Type) (*bindingPlan, error) {
	var fields []fieldBinding
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		tag, source := getBindingTag(field)
		if tag == "" {
			continue
		}
		extractor, err := makeExtractor(field.Type, tag, source)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", field.Name, err)
		}
		fields = append(fields, fieldBinding{idx: i, extractor: extractor})
	}
	return &bindingPlan{fields: fields}, nil
}

func getBindingTag(field reflect.StructField) (tag string, source string) {
	if t := field.Tag.Get("path"); t != "" {
		return t, "path"
	}
	if t := field.Tag.Get("query"); t != "" {
		return t, "query"
	}
	if t := field.Tag.Get("header"); t != "" {
		return t, "header"
	}
	if t := field.Tag.Get("cookie"); t != "" {
		return t, "cookie"
	}
	if t := field.Tag.Get("form"); t != "" {
		return t, "form"
	}
	return "", ""
}

func makeExtractor(ft reflect.Type, tag, source string) (func(*http.Request) (reflect.Value, error), error) {
	switch source {
	case "path":
		return func(r *http.Request) (reflect.Value, error) {
			val := r.PathValue(tag)
			if val == "" {
				return reflect.Value{}, nil
			}
			return convertString(val, ft)
		}, nil

	case "query":
		return func(r *http.Request) (reflect.Value, error) {
			if !r.URL.Query().Has(tag) {
				return reflect.Value{}, nil
			}
			val := r.URL.Query().Get(tag)
			return convertString(val, ft)
		}, nil

	case "header":
		return func(r *http.Request) (reflect.Value, error) {
			val := r.Header.Get(tag)
			if val == "" {
				return reflect.Value{}, nil
			}
			return convertString(val, ft)
		}, nil

	case "cookie":
		return func(r *http.Request) (reflect.Value, error) {
			c, err := r.Cookie(tag)
			if err != nil || c == nil {
				return reflect.Value{}, nil
			}
			return convertString(c.Value, ft)
		}, nil

	case "form":
		return func(r *http.Request) (reflect.Value, error) {
			val := r.FormValue(tag)
			if val == "" {
				return reflect.Value{}, nil
			}
			return convertString(val, ft)
		}, nil

	default:
		return nil, xerr.Internal("unsupported binding source: " + source)
	}
}

func convertString(val string, ft reflect.Type) (reflect.Value, error) {
	if ft == reflect.TypeOf(time.Time{}) {
		layouts := []string{
			time.RFC3339,
			time.RFC3339Nano,
			time.DateOnly,
			time.DateTime,
		}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, val); err == nil {
				return reflect.ValueOf(t), nil
			}
		}
		return reflect.Value{}, xerr.BadRequest(fmt.Sprintf("invalid time format %q (expected RFC3339 or YYYY-MM-DD)", val))
	}

	switch ft.Kind() {
	case reflect.String:
		return reflect.ValueOf(val).Convert(ft), nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return reflect.Value{}, xerr.BadRequest(fmt.Sprintf("invalid integer %q", val))
		}
		return reflect.ValueOf(i).Convert(ft), nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u, err := strconv.ParseUint(val, 10, 64)
		if err != nil {
			return reflect.Value{}, xerr.BadRequest(fmt.Sprintf("invalid unsigned integer %q", val))
		}
		return reflect.ValueOf(u).Convert(ft), nil

	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return reflect.Value{}, xerr.BadRequest(fmt.Sprintf("invalid float %q", val))
		}
		return reflect.ValueOf(f).Convert(ft), nil

	case reflect.Bool:
		b, err := strconv.ParseBool(val)
		if err != nil {
			return reflect.Value{}, xerr.BadRequest(fmt.Sprintf("invalid boolean %q", val))
		}
		return reflect.ValueOf(b).Convert(ft), nil

	default:
		return reflect.Value{}, xerr.Internal(fmt.Sprintf("unsupported target field type: %s", ft.String()))
	}
}
