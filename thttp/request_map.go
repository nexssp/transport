package thttp

import (
	"net/http"
	"reflect"
	"strings"
)

// BindMapFromHTTPRequest adds path and query parameters to a map[string]any
// request. Values remain strings; Kernel Coerce applies the destination action's
// scalar types when the NFlow pipeline forwards this map to a typed action.
// Existing map entries are retained, and JSON body decoding may subsequently
// override a same-named query or path value.
func BindMapFromHTTPRequest(target any, r *http.Request) error {
	if r == nil {
		return nil
	}
	value := reflect.ValueOf(target)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return nil
	}
	value = value.Elem()
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			value.Set(reflect.ValueOf(make(map[string]any)))
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Map || value.Type().Key().Kind() != reflect.String || value.Type().Elem().Kind() != reflect.Interface {
		return nil
	}
	if value.IsNil() {
		value.Set(reflect.MakeMap(value.Type()))
	}
	keyType := value.Type().Key()
	elemType := value.Type().Elem()
	setString := func(key, text string) {
		k := reflect.ValueOf(key).Convert(keyType)
		v := reflect.ValueOf(any(text))
		if v.Type().AssignableTo(elemType) {
			value.SetMapIndex(k, v)
		}
	}

	// Query parameters are first so an explicit path wildcard with the same
	// name remains authoritative. Repeated query keys use the conventional
	// first-value behavior of URL.Query().Get.
	for name := range r.URL.Query() {
		setString(name, r.URL.Query().Get(name))
	}

	// net/http records the selected ServeMux pattern on a routed request. The
	// transport registers a path-only dispatcher pattern, while callers may
	// still use method-qualified patterns on a directly managed ServeMux.
	patternPath := r.Pattern
	if fields := strings.Fields(patternPath); len(fields) >= 2 {
		patternPath = fields[1]
	}
	if patternPath != "" {
		for segment := range strings.SplitSeq(patternPath, "/") {
			if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
				continue
			}
			name := strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}")
			name = strings.TrimSuffix(name, "...")
			if name == "" {
				continue
			}
			if text := r.PathValue(name); text != "" {
				setString(name, text)
			}
		}
	}
	return nil
}
