package thttp

import (
	"fmt"
	"reflect"

	"github.com/nexssp/kernel/action"
)

var decoderType = reflect.TypeFor[HTTPDecoder]()

func validateBindings(actions []action.AnyAction) {
	for _, act := range actions {
		if act == nil {
			continue
		}
		meta := act.Describe()
		for _, b := range act.GetBindings() {
			if _, ok := b.(RawHTTPRoute); ok {
				if meta != nil && meta.Idempotency.Enabled {
					panic(fmt.Sprintf("thttp: action %q cannot combine :idempotent with :raw_route; raw HTTP responses are not cached", meta.Name))
				}
				continue
			}
			if _, ok := b.(HTTPRoute); !ok {
				continue
			}
			typed, ok := act.(action.TypedPayload)
			if !ok {
				continue
			}
			reqVal := typed.ReqPayload()
			if reqVal == nil {
				continue
			}
			actionName := ""
			if meta != nil {
				actionName = meta.Name
			}
			checkDecoder(actionName, reqVal)
		}
	}
}

func checkDecoder(actionName string, reqVal any) {
	t := reflect.TypeOf(reqVal)
	if t == nil {
		return
	}

	types := []reflect.Type{t}
	if t.Kind() != reflect.Pointer {
		types = append(types, reflect.PointerTo(t))
	}

	for _, rt := range types {
		method, exists := rt.MethodByName("FromHTTPRequest")
		if !exists {
			continue
		}
		if rt.Implements(decoderType) {
			return
		}
		sig := methodSignature(method)
		panic(fmt.Sprintf(
			"\n\nthttp: action %q — request type %s has FromHTTPRequest with wrong signature:\n"+
				"  got:  %s\n"+
				"  want: func(r *http.Request) error\n\n"+
				"Fix: implement thttp.HTTPDecoder exactly: var _ thttp.HTTPDecoder = (*%s)(nil)\n",
			actionName, typeName(t), sig, baseTypeName(t),
		))
	}
}

func methodSignature(m reflect.Method) string {
	t := m.Type
	args := make([]string, 0, t.NumIn())
	for i := 1; i < t.NumIn(); i++ {
		args = append(args, t.In(i).String())
	}
	outs := make([]string, 0, t.NumOut())
	for out := range t.Outs() {
		outs = append(outs, out.String())
	}
	return fmt.Sprintf("func(%v) (%v)", args, outs)
}

func typeName(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		return "*" + t.Elem().Name()
	}
	return t.Name()
}

func baseTypeName(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		return t.Elem().Name()
	}
	return t.Name()
}
