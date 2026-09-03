package tcli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transport/tcli"
)

type ComprehensiveReq struct {
	Name       string    `cli:"n,name" usage:"User full name"`
	ConfigPath string    `cli:"config" usage:"Path to YAML configuration"`
	Force      bool      `cli:"f,force" usage:"Force overwrite"`
	Verbose    bool      `cli:"v,verbose" usage:"Verbose output"`
	Count      int       `cli:"count" usage:"Item count"`
	Int8Val    int8      `cli:"i8"`
	Int16Val   int16     `cli:"i16"`
	Int32Val   int32     `cli:"i32"`
	Int64Val   int64     `cli:"i64"`
	UintVal    uint      `cli:"u"`
	Uint8Val   uint8     `cli:"u8"`
	Uint16Val  uint16    `cli:"u16"`
	Uint32Val  uint32    `cli:"u32"`
	Uint64Val  uint64    `cli:"u64"`
	Float32Val float32   `cli:"f32"`
	Float64Val float64   `cli:"f64"`
	Timestamp  time.Time `cli:"time" usage:"Timestamp in RFC3339 format"`
	Tags       []string  `cli:"t,tag" usage:"Tags to apply (repeatable)"`
	Dirs       []string  `cli:"d,dir" usage:"Directories to scan (repeatable)"`
	Files      []string  `cli:",positional"`
}

func TestBinder_AllDataTypes(t *testing.T) {
	t.Parallel()

	var captured ComprehensiveReq

	testAct := action.New("test.alltypes", func(ctx context.Context, req ComprehensiveReq) (string, error) {
		captured = req
		return "ok", nil
	}).Route(tcli.Command("types-test", "Test all types")).Build()

	testTime := time.Date(2026, 9, 3, 15, 30, 0, 0, time.UTC)

	var stdout, stderr bytes.Buffer
	cli := tcli.New(
		tcli.WithArgs(
			"types-test",
			"--name=Alice",
			"--config", "custom.yml",
			"-f",             // Bare bool -> true
			"--verbose=true", // Explicit bool -> true
			"--count=42",
			"--i8=-8", "--i16=-16", "--i32=-32", "--i64=-64",
			"--u=1", "--u8=8", "--u16=16", "--u32=32", "--u64=64",
			"--f32=3.14", "--f64=2.718281828",
			"--time", testTime.Format(time.RFC3339),
			"-t", "go,ts", // Comma-separated slice
			"-t", "security", // Repeated slice accumulation
			"-d", "cmd", "-d", "pkg/auth", // Multiple repeated flags
			"main.go", "config.go", // Positional arguments
		),
		tcli.WithOutput(&stdout, &stderr),
	)
	cli.Mount([]action.AnyAction{testAct})

	_, err := cli.Do(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected Do() error: %v", err)
	}

	// Strings
	if captured.Name != "Alice" || captured.ConfigPath != "custom.yml" {
		t.Errorf("string mismatch: Name=%q, ConfigPath=%q", captured.Name, captured.ConfigPath)
	}
	// Booleans
	if !captured.Force || !captured.Verbose {
		t.Errorf("bool mismatch: Force=%v, Verbose=%v", captured.Force, captured.Verbose)
	}
	// Signed Integers
	if captured.Count != 42 || captured.Int8Val != -8 || captured.Int16Val != -16 || captured.Int32Val != -32 || captured.Int64Val != -64 {
		t.Errorf("signed int mismatch: Count=%d, i8=%d, i16=%d, i32=%d, i64=%d",
			captured.Count, captured.Int8Val, captured.Int16Val, captured.Int32Val, captured.Int64Val)
	}
	// Unsigned Integers
	if captured.UintVal != 1 || captured.Uint8Val != 8 || captured.Uint16Val != 16 || captured.Uint32Val != 32 || captured.Uint64Val != 64 {
		t.Errorf("unsigned int mismatch: u=%d, u8=%d, u16=%d, u32=%d, u64=%d",
			captured.UintVal, captured.Uint8Val, captured.Uint16Val, captured.Uint32Val, captured.Uint64Val)
	}
	// Floats
	if captured.Float32Val < 3.13 || captured.Float32Val > 3.15 || captured.Float64Val < 2.71 || captured.Float64Val > 2.72 {
		t.Errorf("float mismatch: f32=%v, f64=%v", captured.Float32Val, captured.Float64Val)
	}
	// Time
	if !captured.Timestamp.Equal(testTime) {
		t.Errorf("time mismatch: got %v, want %v", captured.Timestamp, testTime)
	}
	// Slices (Accumulated)
	expectedTags := []string{"go", "ts", "security"}
	if len(captured.Tags) != len(expectedTags) {
		t.Fatalf("tags slice length mismatch: got %v, want %v", captured.Tags, expectedTags)
	}
	for i, tag := range expectedTags {
		if captured.Tags[i] != tag {
			t.Errorf("tag[%d]: got %q, want %q", i, captured.Tags[i], tag)
		}
	}
	// Positionals
	expectedFiles := []string{"main.go", "config.go"}
	if len(captured.Files) != len(expectedFiles) || captured.Files[0] != expectedFiles[0] || captured.Files[1] != expectedFiles[1] {
		t.Errorf("positional mismatch: got %v, want %v", captured.Files, expectedFiles)
	}
}

func TestBinder_PositionalStringFallback(t *testing.T) {
	t.Parallel()

	type TextReq struct {
		Text string `cli:",positional"`
	}

	var captured TextReq
	act := action.New("test.stringpos", func(ctx context.Context, req TextReq) (string, error) {
		captured = req
		return "done", nil
	}).Route(tcli.Command("echo", "Echo text")).Build()

	var stdout, stderr bytes.Buffer
	cli := tcli.New(
		tcli.WithArgs("echo", "hello", "world", "from", "cli"),
		tcli.WithOutput(&stdout, &stderr),
	)
	cli.Mount([]action.AnyAction{act})

	_, err := cli.Do(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.Text != "hello world from cli" {
		t.Fatalf("expected joined positional string 'hello world from cli', got %q", captured.Text)
	}
}

func TestBinder_ValidationErrors(t *testing.T) {
	t.Parallel()

	type StrictReq struct {
		Number int       `cli:"num"`
		Bool   bool      `cli:"b"`
		Date   time.Time `cli:"date"`
	}

	act := action.New("test.strict", func(ctx context.Context, req StrictReq) (string, error) {
		return "ok", nil
	}).Route(tcli.Command("strict", "Strict parsing")).Build()

	tests := []struct {
		name        string
		args        []string
		errContains string
	}{
		{
			name:        "Invalid integer",
			args:        []string{"strict", "--num=not_a_number"},
			errContains: "invalid syntax",
		},
		{
			name:        "Invalid boolean",
			args:        []string{"strict", "--b=not_a_bool"},
			errContains: "invalid syntax",
		},
		{
			name:        "Invalid time format",
			args:        []string{"strict", "--date=2026/99/99"},
			errContains: "invalid time format",
		},
		{
			name:        "Unknown Command",
			args:        []string{"non_existent_command"},
			errContains: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			cli := tcli.New(
				tcli.WithArgs(tt.args...),
				tcli.WithOutput(&stdout, &stderr),
			)
			cli.Mount([]action.AnyAction{act})

			_, err := cli.Do(context.Background(), nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.errContains) && !strings.Contains(stderr.String(), tt.errContains) {
				t.Fatalf("expected error containing %q, got err=%v, stderr=%s", tt.errContains, err, stderr.String())
			}
		})
	}
}

func TestBinder_DoubleDashTerminator(t *testing.T) {
	t.Parallel()

	type Req struct {
		Force bool     `cli:"f,force"`
		Files []string `cli:",positional"`
	}

	var captured Req

	act := action.New("test.dashdash", func(_ context.Context, req Req) (string, error) {
		captured = req
		return "ok", nil
	}).Route(tcli.Command("dash", "Dash")).Build()

	var stdout, stderr bytes.Buffer
	cli := tcli.New(
		tcli.WithArgs("dash", "--force", "--", "-not-a-flag", "file.go"),
		tcli.WithOutput(&stdout, &stderr),
	)
	cli.Mount([]action.AnyAction{act})

	if _, err := cli.Do(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !captured.Force {
		t.Fatal("expected force to be true")
	}

	expected := []string{"-not-a-flag", "file.go"}
	if len(captured.Files) != len(expected) ||
		captured.Files[0] != expected[0] ||
		captured.Files[1] != expected[1] {
		t.Fatalf("unexpected positionals: %v", captured.Files)
	}
}

func TestBinder_NegativeNumberAsSeparateValue(t *testing.T) {
	t.Parallel()

	type Req struct {
		Count int     `cli:"count"`
		Ratio float64 `cli:"ratio"`
	}

	var captured Req

	act := action.New("test.negative", func(_ context.Context, req Req) (string, error) {
		captured = req
		return "ok", nil
	}).Route(tcli.Command("neg", "Negative")).Build()

	var stdout, stderr bytes.Buffer
	cli := tcli.New(
		tcli.WithArgs("neg", "--count", "-10", "--ratio", "-3.14"),
		tcli.WithOutput(&stdout, &stderr),
	)
	cli.Mount([]action.AnyAction{act})

	if _, err := cli.Do(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if captured.Count != -10 {
		t.Fatalf("expected Count=-10, got %d", captured.Count)
	}

	if captured.Ratio < -3.15 || captured.Ratio > -3.13 {
		t.Fatalf("expected Ratio around -3.14, got %v", captured.Ratio)
	}
}
