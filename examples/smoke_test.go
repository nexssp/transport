package examples_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var nflowPath string

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests builds the nflow CLI once, runs the test suite, and returns
// the exit code. Splitting the body out of TestMain lets the deferred
// RemoveAll run when the build fails — os.Exit inside TestMain would
// skip it.
func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "nflow-example-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)

	bin := filepath.Join(dir, "nflow")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	build := exec.CommandContext(context.Background(), "go", "build", "-o", bin, "github.com/nexssp/flow/cmd/nflow")
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "build nflow: %v\n", err)
		return 1
	}
	nflowPath = bin

	return m.Run()
}

// bootDeadline covers the cold harness build of the largest example
// (04_full_stack, four @require modules, ~8s on Windows with a warm
// Go module cache) plus several seconds of live server time. Warm
// harness cache returns in under two seconds.
const bootDeadline = 20 * time.Second

// postKillGrace is how long to wait after killing the process tree for
// the OS to release the example's listening socket before the next
// subtest binds it. Windows needs this; Unix usually does not.
const postKillGrace = 500 * time.Millisecond

func TestExamplesBoot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping example boot tests in -short mode")
	}

	examples := []string{
		"01_event_driven_hub.nflow",
		"02_background_tasks.nflow",
		"03_multi_route_api.nflow",
		"04_full_stack.nflow",
		"05_multitransport_composition_nflow/event_bus.nflow",
		"06_server_api_nflow/server_api.nflow",
	}

	for _, name := range examples {
		t.Run(name, func(t *testing.T) {
			bootExample(t, name)
		})
	}
}

// bootExample runs `nflow run <name>` under a deadline and treats a
// clean kill at the deadline as success. The example is a server: it
// bootstraps, binds a port, and blocks. Any exit before the deadline —
// with a non-zero status — is a failure, and its stderr is printed.
func bootExample(t *testing.T, name string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), bootDeadline)
	defer cancel()

	var output bytes.Buffer
	cmd := exec.Command(nflowPath, "run", name) //nolint:noctx // CommandContext cannot kill the harness grandchild
	cmd.Stdout = &output
	cmd.Stderr = &output

	// The CLI spawns the harness as a child. Without a new process
	// group (Unix) or a killable process tree (Windows), killing the
	// CLI leaves the harness running and holding the port.
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	select {
	case err := <-waitErr:
		if err != nil {
			t.Fatalf("nflow run %s exited before deadline: %v\n%s",
				name, err, tail(strings.TrimSpace(output.String()), 24))
		}
		return

	case <-ctx.Done():
		killProcessGroup(cmd.Process.Pid)

		select {
		case <-waitErr:
		case <-time.After(5 * time.Second):
			t.Fatalf("nflow run %s did not reap after kill", name)
		}

		time.Sleep(postKillGrace)
		return
	}
}

func tail(s string, n int) string {
	if s == "" {
		return "(no output)"
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return "...\n" + strings.Join(lines[len(lines)-n:], "\n")
}
