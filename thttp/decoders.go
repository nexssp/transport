package thttp

import (
	"fmt"
	"net/http"
	"reflect"
	"strconv"

	"github.com/nexssp/kernel/xerr"
)

// PathID is a zero-allocation generic path parameter decoder for /{id}.
type PathID[T comparable] struct {
	ID T `json:"id"`
}

func (p *PathID[T]) GetID() T { return p.ID }

func (p *PathID[T]) FromHTTPRequest(r *http.Request) error {
	raw := r.PathValue("id")
	if raw == "" {
		return xerr.BadRequest("missing required path parameter 'id'")
	}

	target := reflect.ValueOf(&p.ID).Elem()
	switch target.Kind() {
	case reflect.String:
		target.SetString(raw)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v <= 0 || target.OverflowInt(v) {
			return xerr.BadRequest("invalid id format")
		}
		target.SetInt(v)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || v == 0 || target.OverflowUint(v) {
			return xerr.BadRequest("invalid id format")
		}
		target.SetUint(v)

	default:
		return fmt.Errorf("unsupported PathID type: %s", target.Type())
	}

	return nil
}

// ListParams is a standardized URL pagination and filter decoder.
type ListParams struct {
	Search string `json:"search" query:"search"`
	Limit  int    `json:"limit" query:"limit"`
	Page   int    `json:"page" query:"page"`
	Sort   string `json:"sort" query:"sort"`
}

func (l *ListParams) FromHTTPRequest(r *http.Request) error {
	q := r.URL.Query()
	l.Search = q.Get("search")
	l.Sort = q.Get("sort")
	l.Limit, _ = strconv.Atoi(q.Get("limit"))
	l.Page, _ = strconv.Atoi(q.Get("page"))

	if l.Limit <= 0 {
		l.Limit = 25
	}
	if l.Page <= 0 {
		l.Page = 1
	}
	return nil
}
