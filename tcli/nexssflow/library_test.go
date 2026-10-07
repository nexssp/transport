package nexssflow

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nexssp/flow/core"
	"github.com/nexssp/flow/extensions/pipeline"
	"github.com/nexssp/flow/extensions/runtime"
	"github.com/nexssp/flow/runner"
	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transport/tcli"
)

const pipelineCLI = `
@pipeline pack:cli="pack:Package source context"
  runtime.const @{ value: "pack-ran" }
@end

tcli.listen
`

func executeCLIFlow(
	t *testing.T,
	source string,
	initial map[string]any,
	args ...string,
) (stdout, stderr string, err error) {
	t.Helper()

	var stdoutBuf, stderrBuf bytes.Buffer
	tr := tcli.New(
		tcli.WithArgs(args...),
		tcli.WithOutput(&stdoutBuf, &stderrBuf),
		tcli.WithExecutable("srcpack"),
	)
	bundle := Bundle(nil)
	bundle.Libraries[0] = action.Library{
		Name: "transport.tcli",
		Actions: []action.AnyAction{
			listenTriggerAction(tr),
		},
	}

	cfg, cfgErr := runner.BuildConfig([]core.Bundle{
		runtime.Bundle(nil),
		pipeline.Bundle(nil),
		bundle,
	})
	if cfgErr != nil {
		return stdoutBuf.String(), stderrBuf.String(), cfgErr
	}
	_, err = runner.Execute(context.Background(), cfg, source, "tcli_integration_test.nflow", initial)
	return stdoutBuf.String(), stderrBuf.String(), err
}

func TestListenAutoDiscoversAndRunsPipelineCommand(t *testing.T) {
	stdout, stderr, err := executeCLIFlow(t, pipelineCLI, nil, "pack")
	if err != nil {
		t.Fatalf("runner.Execute() error = %v; stderr: %s", err, stderr)
	}
	if got := strings.TrimSpace(stdout); got != "pack-ran" {
		t.Fatalf("stdout = %q, want %q", got, "pack-ran")
	}
}

func TestListenAutoDiscoversPipelineCommandHelp(t *testing.T) {
	_, stderr, err := executeCLIFlow(t, pipelineCLI, nil, "pack", "--help")
	if err != nil {
		t.Fatalf("runner.Execute() error = %v; stderr: %s", err, stderr)
	}
	for _, want := range []string{"Command: pack", "Package source context", "Usage:"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("help output %q does not contain %q", stderr, want)
		}
	}
}

func TestListenPreservesExplicitWorkloadOverride(t *testing.T) {
	var stdout, stderr bytes.Buffer
	tr := tcli.New(
		tcli.WithArgs("manual"),
		tcli.WithOutput(&stdout, &stderr),
		tcli.WithExecutable("srcpack"),
	)
	manual := action.New("manual.echo", func(context.Context, struct{}) (string, error) {
		return "manual-ran", nil
	}).Route(tcli.Command("manual", "Manual command")).Build()
	initial := action.Library{Name: "manual", Actions: []action.AnyAction{manual}}

	_, err := action.InvokeAny(context.Background(), listenTriggerAction(tr), initial)
	if err != nil {
		t.Fatalf("listenTriggerAction() error = %v; stderr: %s", err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "manual-ran" {
		t.Fatalf("stdout = %q, want %q", got, "manual-ran")
	}
}

func TestListenReportsWhenNoCLICommandsAreAvailable(t *testing.T) {
	_, _, err := executeCLIFlow(t, "tcli.listen", nil)
	if err == nil {
		t.Fatal("runner.Execute() error = nil, want no CLI-bound actions error")
	}
	if !strings.Contains(err.Error(), "auto-discovery found 0 CLI-bound actions") {
		t.Fatalf("error = %v, want actionable auto-discovery diagnostic", err)
	}
}
