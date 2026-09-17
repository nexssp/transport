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

	for i := range rt.NumField() {
		fieldVal := rv.Field(i)
		if !fieldVal.CanSet() {
			continue
		}

		paramVal := values.Get(queryTag(rt.Field(i)))
		if paramVal == "" {
			continue
		}

		setQueryField(fieldVal, paramVal)
	}
	return nil
}

// queryTag resolves the query parameter name for a struct field, honoring
// the `query` tag first, then `json`, then the lowercased field name.
func queryTag(field reflect.StructField) string {
	tag := field.Tag.Get("query")
	if tag == "" {
		tag = field.Tag.Get("json")
	}
	if tag == "" || tag == "-" {
		return strings.ToLower(field.Name)
	}
	return strings.Split(tag, ",")[0]
}

func setQueryField(fieldVal reflect.Value, paramVal string) {
	switch fieldVal.Kind() { //nolint:exhaustive // only query-string-convertible kinds are supported
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
