package nexssflow

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nexssp/flow/core"
	"github.com/nexssp/flow/extensions/pipeline"
	"github.com/nexssp/flow/extensions/runtime"
	flowschema "github.com/nexssp/flow/extensions/schema"
	"github.com/nexssp/flow/runner"
	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/transport/tcli"
)

// ─────────────────────────────────────────────────────────────────────
// Fixtures
// ─────────────────────────────────────────────────────────────────────

type schemaCLIRequest struct {
	Targets  []string `json:"targets"`
	Tests    bool     `json:"tests"`
	MaxLines int      `json:"max_lines"`
}

const pipelineWithSchemaCLI = `
@schema PackConfig struct {
  Targets []string ` + "`json:\"targets\" cli:\"target,targets,t\" usage:\"source paths to include\" validate:\"required\"`" + `
  Tests bool ` + "`json:\"tests\" cli:\"tests\" usage:\"include tests\"`" + `
  MaxLines int ` + "`json:\"max_lines\" cli:\"max-lines,L\" usage:\"maximum source lines\"`" + `
}

@pipeline pack:cli="pack:Package source context":schema=PackConfig
  probe.capture
@end

tcli.listen
`

const schemaWithoutCLITags = `
@schema NoCLI struct {
  Name string ` + "`json:\"name\"`" + `
}

@pipeline pack:cli="pack:No CLI schema":schema=NoCLI
  probe.capture
@end

tcli.listen
`

const schemaUnknownReference = `
@pipeline pack:cli="pack:Unknown schema":schema=DoesNotExist
  probe.capture
@end

tcli.listen
`

const schemaAliasCollision = `
@schema Colliding struct {
  A string ` + "`json:\"a\" cli:\"shared\"`" + `
  B string ` + "`json:\"b\" cli:\"shared\"`" + `
}

@pipeline pack:cli="pack:Collision":schema=Colliding
  probe.capture
@end

tcli.listen
`

const schemaWithPositional = `
@schema WithPositional struct {
  Files []string ` + "`json:\"files\" cli:\",positional\"`" + `
  Ext   string   ` + "`json:\"ext\" cli:\"ext\"`" + `
}

@pipeline collect:cli="collect:Collect files":schema=WithPositional
  probe.capture
@end

tcli.listen
`

// ─────────────────────────────────────────────────────────────────────
// Test harness
// ─────────────────────────────────────────────────────────────────────

type schemaRunResult struct {
	captured schemaCLIRequest
	stdout   string
	stderr   string
	err      error
}

func runSchemaFlow(t *testing.T, source string, args ...string) schemaRunResult {
	t.Helper()

	var captured schemaCLIRequest
	probeAction := action.New("probe.capture", func(_ context.Context, req schemaCLIRequest) (schemaCLIRequest, error) {
		captured = req
		return req, nil
	}).Build()
	probeBundle := core.Bundle{
		ID: "schema-cli-test-probe",
		Libraries: []action.Library{{
			Name:    "schema-cli-test-probe",
			Actions: []action.AnyAction{probeAction},
		}},
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	tr := tcli.New(
		tcli.WithArgs(args...),
		tcli.WithOutput(&stdoutBuf, &stderrBuf),
		tcli.WithExecutable("srcpack"),
	)
	bundle := Bundle(nil)
	bundle.Libraries[0] = action.Library{
		Name:    "transport.tcli",
		Actions: []action.AnyAction{listenTriggerAction(tr)},
	}

	cfg, cfgErr := runner.BuildConfig([]core.Bundle{
		runtime.Bundle(nil),
		pipeline.Bundle(nil),
		flowschema.Bundle(nil),
		bundle,
		probeBundle,
	})
	if cfgErr != nil {
		return schemaRunResult{
			stdout: stdoutBuf.String(),
			stderr: stderrBuf.String(),
			err:    cfgErr,
		}
	}
	_, runErr := runner.Execute(context.Background(), cfg, source, "schema_cli_test.nflow", nil)
	return schemaRunResult{
		captured: captured,
		stdout:   stdoutBuf.String(),
		stderr:   stderrBuf.String(),
		err:      runErr,
	}
}

func requireSuccess(t *testing.T, got schemaRunResult) {
	t.Helper()
	if got.err != nil {
		t.Fatalf("Execute() error = %v\nstdout:\n%s\nstderr:\n%s",
			got.err, got.stdout, got.stderr)
	}
}

func requireFailure(t *testing.T, got schemaRunResult, needles ...string) {
	t.Helper()
	if got.err == nil {
		t.Fatalf("Execute() error = nil, want failure\nstdout:\n%s\nstderr:\n%s",
			got.stdout, got.stderr)
	}
	combined := got.err.Error() + "\n" + got.stderr
	for _, needle := range needles {
		if !strings.Contains(combined, needle) {
			t.Errorf("failure output does not mention %q\ncombined:\n%s", needle, combined)
		}
	}
}

func requireTargets(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Targets = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Targets[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// ─────────────────────────────────────────────────────────────────────
// Happy path
// ─────────────────────────────────────────────────────────────────────

func TestSchemaCLI_HappyPath_AllFlags(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI,
		"pack",
		"--target", "./src",
		"-t", "./lib",
		"--tests",
		"--max-lines", "42",
	)
	requireSuccess(t, got)
	requireTargets(t, got.captured.Targets, []string{"./src", "./lib"})
	if !got.captured.Tests {
		t.Errorf("Tests = false, want true")
	}
	if got.captured.MaxLines != 42 {
		t.Errorf("MaxLines = %d, want 42", got.captured.MaxLines)
	}
}

func TestSchemaCLI_HappyPath_DefaultsForOmittedFields(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "./src")
	requireSuccess(t, got)
	requireTargets(t, got.captured.Targets, []string{"./src"})
	if got.captured.Tests {
		t.Errorf("Tests = true, want false (zero value)")
	}
	if got.captured.MaxLines != 0 {
		t.Errorf("MaxLines = %d, want 0 (zero value)", got.captured.MaxLines)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Slices
// ─────────────────────────────────────────────────────────────────────

func TestSchemaCLI_SliceCommaSeparated(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "a,b,c")
	requireSuccess(t, got)
	requireTargets(t, got.captured.Targets, []string{"a", "b", "c"})
}

func TestSchemaCLI_SliceRepeated(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "-t", "a", "-t", "b", "-t", "c")
	requireSuccess(t, got)
	requireTargets(t, got.captured.Targets, []string{"a", "b", "c"})
}

func TestSchemaCLI_SliceMixedCommaAndRepeat(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "a,b", "-t", "c,d")
	requireSuccess(t, got)
	requireTargets(t, got.captured.Targets, []string{"a", "b", "c", "d"})
}

func TestSchemaCLI_SliceWhitespaceTrimmed(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", " a , b ")
	requireSuccess(t, got)
	requireTargets(t, got.captured.Targets, []string{"a", "b"})
}

// ─────────────────────────────────────────────────────────────────────
// Booleans
// ─────────────────────────────────────────────────────────────────────

func TestSchemaCLI_BoolBare(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "x", "--tests")
	requireSuccess(t, got)
	if !got.captured.Tests {
		t.Errorf("Tests = false, want true")
	}
}

func TestSchemaCLI_BoolEqualsTrue(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "x", "--tests=true")
	requireSuccess(t, got)
	if !got.captured.Tests {
		t.Errorf("Tests = false, want true")
	}
}

func TestSchemaCLI_BoolEqualsFalse(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "x", "--tests=false")
	requireSuccess(t, got)
	if got.captured.Tests {
		t.Errorf("Tests = true, want false")
	}
}

func TestSchemaCLI_BoolSeparateTokenTrue(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "x", "--tests", "true")
	requireSuccess(t, got)
	if !got.captured.Tests {
		t.Errorf("Tests = false, want true")
	}
}

func TestSchemaCLI_BoolLastWins(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "x", "--tests", "--tests=false")
	requireSuccess(t, got)
	if got.captured.Tests {
		t.Errorf("Tests = true, want false (last wins)")
	}
}

// ─────────────────────────────────────────────────────────────────────
// Ints
// ─────────────────────────────────────────────────────────────────────

func TestSchemaCLI_IntPositive(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "x", "--max-lines", "100")
	requireSuccess(t, got)
	if got.captured.MaxLines != 100 {
		t.Errorf("MaxLines = %d, want 100", got.captured.MaxLines)
	}
}

func TestSchemaCLI_IntNegativeSeparate(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "x", "--max-lines", "-1")
	requireSuccess(t, got)
	if got.captured.MaxLines != -1 {
		t.Errorf("MaxLines = %d, want -1", got.captured.MaxLines)
	}
}

func TestSchemaCLI_IntNegativeEquals(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "x", "--max-lines=-1")
	requireSuccess(t, got)
	if got.captured.MaxLines != -1 {
		t.Errorf("MaxLines = %d, want -1", got.captured.MaxLines)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Short aliases
// ─────────────────────────────────────────────────────────────────────

func TestSchemaCLI_ShortAliases(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "-t", "x", "-L", "5")
	requireSuccess(t, got)
	requireTargets(t, got.captured.Targets, []string{"x"})
	if got.captured.MaxLines != 5 {
		t.Errorf("MaxLines = %d, want 5", got.captured.MaxLines)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Bad paths
// ─────────────────────────────────────────────────────────────────────

func TestSchemaCLI_MissingRequiredField(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--tests")
	requireFailure(t, got, "required")
}

func TestSchemaCLI_MissingRequiredFieldWithEmptyArgs(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack")
	requireFailure(t, got, "required")
}

func TestSchemaCLI_UnknownFlag(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "x", "--nope", "y")
	requireFailure(t, got, "unknown flag")
}

func TestSchemaCLI_ShortUnknownFlag(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "x", "-z")
	requireFailure(t, got, "unknown flag")
}

func TestSchemaCLI_ValueLooksLikeFlag(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "--tests")
	requireFailure(t, got, "requires a value")
}

func TestSchemaCLI_UnexpectedPositional(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "x", "loose-arg")
	requireFailure(t, got, "positional")
}

func TestSchemaCLI_InvalidBoolValue(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "x", "--tests=maybe")
	requireFailure(t, got, "invalid syntax")
}

func TestSchemaCLI_InvalidIntValue(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "x", "--max-lines", "abc")
	requireFailure(t, got, "invalid syntax")
}

// ─────────────────────────────────────────────────────────────────────
// Positional field
// ─────────────────────────────────────────────────────────────────────

func TestSchemaCLI_PositionalSliceAccumulates(t *testing.T) {
	got := runSchemaFlow(t, schemaWithPositional,
		"collect",
		"--ext", "go",
		"a.go",
		"b.go",
	)
	requireSuccess(t, got)
	if got.captured.Targets != nil {
		t.Errorf("Targets = %#v, want nil for positional schema", got.captured.Targets)
	}
}

func TestSchemaCLI_DoubleDashTerminatorPassesRestToPositional(t *testing.T) {
	got := runSchemaFlow(t, schemaWithPositional,
		"collect",
		"--ext", "go",
		"--",
		"-not-a-flag",
		"file.go",
	)
	requireSuccess(t, got)
}

// ─────────────────────────────────────────────────────────────────────
// Schema contract violations
// ─────────────────────────────────────────────────────────────────────

func TestSchemaCLI_SchemaWithoutCLITags_Rejected(t *testing.T) {
	got := runSchemaFlow(t, schemaWithoutCLITags, "pack")
	requireFailure(t, got, "no fields")
}

func TestSchemaCLI_UnknownSchemaReference_Rejected(t *testing.T) {
	got := runSchemaFlow(t, schemaUnknownReference, "pack")
	requireFailure(t, got, "DoesNotExist")
}

func TestSchemaCLI_AliasCollision_Rejected(t *testing.T) {
	got := runSchemaFlow(t, schemaAliasCollision, "pack")
	requireFailure(t, got, "assigned to both")
}

// ─────────────────────────────────────────────────────────────────────
// Help rendering
// ─────────────────────────────────────────────────────────────────────

func TestSchemaCLI_Help_RendersAllFlagsAndUsages(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--help")
	requireSuccess(t, got)

	for _, want := range []string{
		"Command: pack",
		"Package source context",
		"Usage:",
		"--target",
		"-t",
		"--tests",
		"--max-lines",
		"-L",
		"source paths to include",
		"include tests",
		"maximum source lines",
		"(required)",
	} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("help output does not contain %q\nstderr:\n%s", want, got.stderr)
		}
	}
}

func TestSchemaCLI_Help_DoesNotExecuteAction(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--help")
	requireSuccess(t, got)

	if len(got.captured.Targets) != 0 || got.captured.Tests || got.captured.MaxLines != 0 {
		t.Errorf("action ran during --help; captured = %+v", got.captured)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Combinations
// ─────────────────────────────────────────────────────────────────────

func TestSchemaCLI_AllFlagsCombined_LastWinsForScalars(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI,
		"pack",
		"-t", "a",
		"--target", "b,c",
		"-L", "10",
		"--max-lines=20",
		"--tests",
	)
	requireSuccess(t, got)
	requireTargets(t, got.captured.Targets, []string{"a", "b", "c"})
	if got.captured.MaxLines != 20 {
		t.Errorf("MaxLines = %d, want 20 (last wins)", got.captured.MaxLines)
	}
	if !got.captured.Tests {
		t.Errorf("Tests = false, want true")
	}
}

// ─────────────────────────────────────────────────────────────────────
// Error classification
// ─────────────────────────────────────────────────────────────────────

func TestSchemaCLI_MissingRequired_IsBadRequest(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack")
	if got.err == nil {
		t.Fatal("expected error")
	}
	if kind := xerr.KindFrom(got.err); kind != xerr.KindBadRequest {
		t.Errorf("KindFrom(error) = %v, want KindBadRequest\nerror: %v", kind, got.err)
	}
}

func TestSchemaCLI_UnknownFlag_IsBadRequest(t *testing.T) {
	got := runSchemaFlow(t, pipelineWithSchemaCLI, "pack", "--target", "x", "--nope", "y")
	if got.err == nil {
		t.Fatal("expected error")
	}
	if kind := xerr.KindFrom(got.err); kind != xerr.KindBadRequest {
		t.Errorf("KindFrom(error) = %v, want KindBadRequest\nerror: %v", kind, got.err)
	}
}
