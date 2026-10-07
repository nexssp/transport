package tcli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nexssp/kernel/xtest/ktest"
)

func TestBindCLIRequestTargetBuildsTypedMap(t *testing.T) {
	schema := CLIRequestSchema{
		Name: "PackConfig",
		Fields: []CLIFieldSpec{
			{Name: "targets", Type: "[]string", Flags: []string{"target", "targets", "t"}},
			{Name: "tests", Type: "bool", Flags: []string{"tests"}},
			{Name: "max_lines", Type: "int", Flags: []string{"max-lines"}},
		},
	}
	var request any
	err := bindCLIRequestTarget(&request, []string{
		"--target", "./src",
		"-t", "./lib",
		"--tests",
		"--max-lines", "42",
	}, schema)
	if err != nil {
		t.Fatalf("bindCLIRequestTarget() error = %v", err)
	}
	got, ok := request.(map[string]any)
	if !ok {
		t.Fatalf("request type = %T, want map[string]any", request)
	}
	if want := []string{"./src", "./lib"}; !reflect.DeepEqual(got["targets"], want) {
		t.Errorf("targets = %#v, want %#v", got["targets"], want)
	}
	if got["tests"] != true {
		t.Errorf("tests = %#v, want true", got["tests"])
	}
	if got["max_lines"] != int64(42) {
		t.Errorf("max_lines = %#v, want int64(42)", got["max_lines"])
	}
}

func TestParseCLIRequestEnforcesRequiredAndKnownFlags(t *testing.T) {
	schema := CLIRequestSchema{
		Name: "Input",
		Fields: []CLIFieldSpec{{
			Name:     "target",
			Type:     "string",
			Flags:    []string{"target"},
			Required: true,
		}},
	}
	if _, err := parseCLIRequest(nil, schema); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing required field error = %v, want required-field error", err)
	}
	if _, err := parseCLIRequest([]string{"--unknown", "x"}, schema); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("unknown flag error = %v, want unknown-flag error", err)
	}
}

func TestDynamicRequestWithoutSchemaRejectsArguments(t *testing.T) {
	var request any
	err := bindCLITarget(&request, []string{"--target", "./src"})
	if err == nil || !strings.Contains(err.Error(), "no CLI schema") {
		t.Fatalf("bindCLITarget() error = %v, want missing-schema diagnostic", err)
	}
}

func TestParseCLIRequest_ValidatesSchema(t *testing.T) {
	t.Parallel()

	schema := CLIRequestSchema{
		Name: "Input",
		Fields: []CLIFieldSpec{
			{Name: "a", Type: "string", Flags: []string{"x"}},
			{Name: "b", Type: "string", Flags: []string{"x"}},
		},
	}

	_, err := ParseCLIRequest([]string{"--x", "v"}, schema)
	if err == nil || !strings.Contains(err.Error(), "assigned to both") {
		t.Fatalf("ParseCLIRequest() error = %v, want ambiguous-alias diagnostic", err)
	}
}

func TestParseCLIRequest_ReturnsTypedPayload(t *testing.T) {
	t.Parallel()

	schema := CLIRequestSchema{
		Name: "Input",
		Fields: []CLIFieldSpec{
			{Name: "name", Type: "string", Flags: []string{"name"}},
			{Name: "verbose", Type: "bool", Flags: []string{"v"}},
		},
	}

	got, err := ParseCLIRequest([]string{"--name", "x", "-v"}, schema)
	if err != nil {
		t.Fatalf("ParseCLIRequest() error = %v", err)
	}
	want := map[string]any{"name": "x", "verbose": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("payload = %#v, want %#v", got, want)
	}
}

func TestParseCLIRequest_ValueForms(t *testing.T) {
	schema := CLIRequestSchema{
		Name: "Input",
		Fields: []CLIFieldSpec{
			{Name: "tags", Type: "[]string", Flags: []string{"tag", "t"}},
			{Name: "ext", Type: "string", Flags: []string{"ext"}},
			{Name: "verbose", Type: "bool", Flags: []string{"v", "verbose"}},
			{Name: "limit", Type: "int", Flags: []string{"limit"}},
			{Name: "ratio", Type: "float64", Flags: []string{"ratio"}},
			{Name: "path", Type: "string", Flags: []string{"path"}},
		},
	}

	tests := []struct {
		name    string
		args    []string
		want    map[string]any
		wantErr string
	}{
		// bool — four accepted spellings
		{name: "bool bare", args: []string{"--verbose"}, want: map[string]any{"verbose": true}},
		{name: "bool equals true", args: []string{"--verbose=true"}, want: map[string]any{"verbose": true}},
		{name: "bool equals false", args: []string{"--verbose=false"}, want: map[string]any{"verbose": false}},
		{name: "bool separate true", args: []string{"--verbose", "true"}, want: map[string]any{"verbose": true}},
		{name: "bool separate false", args: []string{"--verbose", "false"}, want: map[string]any{"verbose": false}},
		{name: "bool short bare", args: []string{"-v"}, want: map[string]any{"verbose": true}},
		{name: "bool last wins", args: []string{"--verbose", "--verbose=false"}, want: map[string]any{"verbose": false}},

		// string
		{name: "string separate", args: []string{"--ext", "go"}, want: map[string]any{"ext": "go"}},
		{name: "string equals", args: []string{"--ext=go"}, want: map[string]any{"ext": "go"}},
		{name: "string last wins", args: []string{"--ext", "go", "--ext", "ts"}, want: map[string]any{"ext": "ts"}},
		{name: "string value looks like flag", args: []string{"--ext", "-x"}, wantErr: "requires a value"},

		// slice
		{name: "slice repeated", args: []string{"--tag", "a", "--tag", "b"}, want: map[string]any{"tags": []string{"a", "b"}}},
		{name: "slice comma", args: []string{"--tag", "a,b"}, want: map[string]any{"tags": []string{"a", "b"}}},
		{name: "slice mixed", args: []string{"--tag", "a,b", "--tag", "c"}, want: map[string]any{"tags": []string{"a", "b", "c"}}},
		{name: "slice short alias", args: []string{"-t", "a"}, want: map[string]any{"tags": []string{"a"}}},
		{name: "slice equals form", args: []string{"--tag=a,b"}, want: map[string]any{"tags": []string{"a", "b"}}},
		{name: "slice whitespace trimmed", args: []string{"--tag", " a , b "}, want: map[string]any{"tags": []string{"a", "b"}}},

		// int / float
		{name: "int separate", args: []string{"--limit", "42"}, want: map[string]any{"limit": int64(42)}},
		{name: "int equals", args: []string{"--limit=42"}, want: map[string]any{"limit": int64(42)}},
		{name: "int negative separate", args: []string{"--limit", "-1"}, want: map[string]any{"limit": int64(-1)}},
		{name: "int negative equals", args: []string{"--limit=-1"}, want: map[string]any{"limit": int64(-1)}},
		{name: "int invalid", args: []string{"--limit", "abc"}, wantErr: "invalid syntax"},
		{name: "float", args: []string{"--ratio", "2.5"}, want: map[string]any{"ratio": 2.5}},

		// flag terminators and positionals
		{name: "double dash makes rest positional", args: []string{"--", "--verbose"}, wantErr: "unexpected positional"},
		{name: "unknown flag rejected", args: []string{"--missing"}, wantErr: "unknown flag"},
		{name: "short unknown flag rejected", args: []string{"-z"}, wantErr: "unknown flag"},

		// empty value semantics
		{name: "string equals empty produces empty", args: []string{"--ext="}, wantErr: "requires a value"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseCLIRequest(test.args, schema)
			if test.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (payload %#v)", test.wantErr, got)
				}
				ktest.RequireStringContains(t, err.Error(), test.wantErr)
				return
			}
			ktest.RequireNoError(t, err)
			ktest.RequireEqual(t, got, test.want)
		})
	}
}

func TestParseCLIRequest_PositionalContract(t *testing.T) {
	t.Parallel()

	withPositional := CLIRequestSchema{
		Name: "Input",
		Fields: []CLIFieldSpec{
			{Name: "files", Type: "[]string", Positional: true},
			{Name: "ext", Type: "string", Flags: []string{"ext"}},
		},
	}
	withoutPositional := CLIRequestSchema{
		Name: "Input",
		Fields: []CLIFieldSpec{
			{Name: "ext", Type: "string", Flags: []string{"ext"}},
		},
	}

	t.Run("positional slice accumulates every bare argument", func(t *testing.T) {
		t.Parallel()
		got, err := parseCLIRequest([]string{"a.go", "--ext", "go", "b.go"}, withPositional)
		ktest.RequireNoError(t, err)
		ktest.RequireEqual(t, got, map[string]any{
			"files": []string{"a.go", "b.go"},
			"ext":   "go",
		})
	})

	t.Run("no positional field rejects bare argument", func(t *testing.T) {
		t.Parallel()
		_, err := parseCLIRequest([]string{"a.go"}, withoutPositional)
		ktest.RequireStringContains(t, err.Error(), "unexpected positional argument")
	})

	t.Run("double dash routes rest to positional field", func(t *testing.T) {
		t.Parallel()
		got, err := parseCLIRequest([]string{"--", "-not-a-flag", "file.go"}, withPositional)
		ktest.RequireNoError(t, err)
		ktest.RequireEqual(t, got, map[string]any{
			"files": []string{"-not-a-flag", "file.go"},
		})
	})
}

func TestParseCLIRequest_RequiredAndUnknown(t *testing.T) {
	t.Parallel()

	schema := CLIRequestSchema{
		Name: "Input",
		Fields: []CLIFieldSpec{
			{Name: "name", Type: "string", Flags: []string{"name"}, Required: true},
			{Name: "count", Type: "int", Flags: []string{"count"}},
		},
	}

	t.Run("missing required field", func(t *testing.T) {
		t.Parallel()
		_, err := parseCLIRequest([]string{"--count", "3"}, schema)
		ktest.RequireStringContains(t, err.Error(), "missing required CLI field")
	})

	t.Run("present required field", func(t *testing.T) {
		t.Parallel()
		got, err := parseCLIRequest([]string{"--name", "x"}, schema)
		ktest.RequireNoError(t, err)
		ktest.RequireEqual(t, got, map[string]any{"name": "x"})
	})

	t.Run("unknown flag names the offending key", func(t *testing.T) {
		t.Parallel()
		_, err := parseCLIRequest([]string{"--name", "x", "--typo", "y"}, schema)
		ktest.RequireStringContains(t, err.Error(), "unknown flag: -typo")
	})
}
