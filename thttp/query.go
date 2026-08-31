package thttp

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

// Query[T] fills any struct T from URL query parameters automatically.
type Query[T any] struct {
	Data T
}

func (q *Query[T]) FromHTTPRequest(r *http.Request) error {
	values := r.URL.Query()
	rv := reflect.ValueOf(&q.Data).Elem()
	rt := rv.Type()

	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		fieldVal := rv.Field(i)

		if !fieldVal.CanSet() {
			continue
		}

		tag := field.Tag.Get("query")
		if tag == "" {
			tag = field.Tag.Get("json")
		}
		if tag == "" || tag == "-" {
			tag = strings.ToLower(field.Name)
		} else {
			tag = strings.Split(tag, ",")[0]
		}

		paramVal := values.Get(tag)
		if paramVal == "" {
			continue
		}

		switch fieldVal.Kind() {
		case reflect.String:
			fieldVal.SetString(paramVal)
		case reflect.Int, reflect.Int64:
			if v, err := strconv.ParseInt(paramVal, 10, 64); err == nil {
				fieldVal.SetInt(v)
			}
		case reflect.Uint, reflect.Uint64:
			if v, err := strconv.ParseUint(paramVal, 10, 64); err == nil {
				fieldVal.SetUint(v)
			}
		case reflect.Float64:
			if v, err := strconv.ParseFloat(paramVal, 64); err == nil {
				fieldVal.SetFloat(v)
			}
		case reflect.Bool:
			fieldVal.SetBool(paramVal == "true" || paramVal == "1")
		}
	}
	return nil
}
