package thttp

import (
	"reflect"
	"regexp"
	"strings"
)

// InferRoute analyzes a Request struct type and generates standard RESTful routes at boot time.
func InferRoute(reqType reflect.Type) HTTPRoute {
	for reqType.Kind() == reflect.Pointer {
		reqType = reqType.Elem()
	}
	name := reqType.Name()

	resource := strings.TrimSuffix(name, "Req")
	resource = strings.TrimSuffix(resource, "Request")
	resource = strings.TrimSuffix(resource, "Input")

	var method, path string
	hasID := hasIDField(reqType)

	switch {
	case strings.HasPrefix(resource, "Create"):
		method = "POST"
		path = toKebab(pluralize(strings.TrimPrefix(resource, "Create")))
	case strings.HasPrefix(resource, "List"):
		method = "GET"
		path = toKebab(pluralize(strings.TrimPrefix(resource, "List")))
	case strings.HasPrefix(resource, "Get"):
		method = "GET"
		res := strings.TrimPrefix(resource, "Get")
		path = toKebab(pluralize(res))
		if hasID {
			path += "/{id}"
		}
	case strings.HasPrefix(resource, "Update"):
		method = "PUT"
		res := strings.TrimPrefix(resource, "Update")
		path = toKebab(pluralize(res))
		if hasID {
			path += "/{id}"
		}
	case strings.HasPrefix(resource, "Delete"):
		method = "DELETE"
		res := strings.TrimPrefix(resource, "Delete")
		path = toKebab(pluralize(res))
		if hasID {
			path += "/{id}"
		}
	default:
		method = "POST"
		path = toKebab(resource)
	}

	return HTTPRoute{Method: method, Path: "/" + path}
}

func hasIDField(t reflect.Type) bool {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Name == "ID" || f.Name == "Id" || f.Name == "UUID" || f.Tag.Get("path") == "id" {
			return true
		}
	}
	return false
}

func toKebab(s string) string {
	matchFirstCap := regexp.MustCompile("(.)([A-Z][a-z]+)")
	matchAllCap := regexp.MustCompile("([a-z0-9])([A-Z])")
	kebab := matchFirstCap.ReplaceAllString(s, "${1}-${2}")
	kebab = matchAllCap.ReplaceAllString(kebab, "${1}-${2}")
	return strings.ToLower(kebab)
}

func pluralize(s string) string {
	if strings.HasSuffix(s, "s") {
		return s
	}
	return s + "s"
}
