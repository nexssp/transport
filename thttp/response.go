package thttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
)

// Response describes an HTTP response returned by a typed action. Body values
// that are nil or zero according to reflect.Value.IsZero are treated as absent,
// so Response[int]{Body: 0}, Response[bool]{Body: false}, Response[string]{Body: ""},
// and zero-value structs produce no body. Use a non-nil pointer to explicitly
// encode a scalar zero (for example, n := 0; Response[*int]{Body: &n}). Non-nil
// empty maps and slices are not zero values and are encoded as {} and [].
type Response[T any] struct {
	Status     int
	Headers    http.Header
	SetCookies []*http.Cookie
	Body       T
}

// WithCookie returns a copy of the response with cookie appended to SetCookies.
func (response Response[T]) WithCookie(cookie *http.Cookie) Response[T] {
	if cookie != nil {
		response.SetCookies = append(append([]*http.Cookie(nil), response.SetCookies...), cookie)
	}
	return response
}

// WithHeader returns a copy of the response with the named header set to value.
func (response Response[T]) WithHeader(key, value string) Response[T] {
	response.Headers = response.Headers.Clone()
	if response.Headers == nil {
		response.Headers = make(http.Header)
	}
	response.Headers.Set(key, value)
	return response
}

// WithStatus returns a copy of the response with status set.
func (response Response[T]) WithStatus(status int) Response[T] {
	response.Status = status
	return response
}

// IsEmptyBody reports whether WriteResponseTo omits Body for the given method.
func (response Response[T]) IsEmptyBody(method string) bool {
	return method == http.MethodHead || statusHasNoBody(response.Status) || isEmptyResponseBody(response.Body)
}

// WriteResponseTo writes the response, JSON-encoding Body when present.
func (response Response[T]) WriteResponseTo(writer http.ResponseWriter) error {
	return response.writeResponseTo(writer, http.StatusOK, "")
}

func (response Response[T]) writeResponseTo(writer http.ResponseWriter, fallbackStatus int, method string) error {
	if writer == nil {
		return errors.New("thttp: response writer is nil")
	}

	status := response.Status
	if status == 0 {
		status = fallbackStatus
	}
	if status == 0 {
		status = http.StatusOK
	}
	if status < 100 || status > 599 {
		return fmt.Errorf("thttp: invalid HTTP response status %d", status)
	}

	bodyIsEmpty := isEmptyResponseBody(response.Body)
	statusDisallowsBody := statusHasNoBody(status)
	emptyBody := method == http.MethodHead || statusDisallowsBody || bodyIsEmpty
	var body []byte
	var err error
	if !emptyBody {
		body, err = json.Marshal(response.Body)
		if err != nil {
			return fmt.Errorf("thttp: encode typed HTTP response body: %w", err)
		}
	}

	for key, values := range response.Headers {
		writer.Header().Del(key)
		for _, value := range values {
			writer.Header().Add(key, value)
		}
	}
	for _, cookie := range response.SetCookies {
		if cookie != nil {
			http.SetCookie(writer, cookie)
		}
	}

	if statusDisallowsBody || bodyIsEmpty {
		writer.Header().Del("Content-Type")
		writer.Header().Del("Content-Length")
	} else if writer.Header().Get("Content-Type") == "" {
		writer.Header().Set("Content-Type", "application/json")
	}

	writer.WriteHeader(status)
	if emptyBody {
		return nil
	}
	if _, err := writer.Write(body); err != nil {
		return fmt.Errorf("thttp: write typed HTTP response body: %w", err)
	}
	return nil
}

func statusHasNoBody(status int) bool {
	return (status >= 100 && status < 200) || status == http.StatusNoContent || status == http.StatusNotModified
}

func isEmptyResponseBody(body any) bool {
	if body == nil {
		return true
	}
	value := reflect.ValueOf(body)
	return value.IsZero()
}

type typedHTTPResponse interface {
	WriteResponseTo(http.ResponseWriter) error
	writeResponseTo(http.ResponseWriter, int, string) error
}

func isTypedHTTPResponse(value any) bool {
	typeOf := reflect.TypeOf(value)
	return typeOf != nil && typeOf.Implements(reflect.TypeFor[typedHTTPResponse]())
}

func writeTypedHTTPResponse(writer http.ResponseWriter, result any, fallbackStatus int, method string) error {
	value := reflect.ValueOf(result)
	if value.IsValid() && value.Kind() == reflect.Pointer && value.IsNil() {
		status := fallbackStatus
		if status == 0 {
			status = http.StatusOK
		}
		if status < 100 || status > 599 {
			return fmt.Errorf("thttp: invalid HTTP response status %d", status)
		}
		writer.WriteHeader(status)
		return nil
	}
	response, ok := result.(typedHTTPResponse)
	if !ok {
		return fmt.Errorf("thttp: expected typed Response[T], got %T", result)
	}
	return response.writeResponseTo(writer, fallbackStatus, method)
}
