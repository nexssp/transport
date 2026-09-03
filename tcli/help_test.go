package tcli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transport/tcli"
)

type HelpSampleReq struct {
	Name    string   `cli:"n,name" usage:"User full name"`
	Force   bool     `cli:"f,force" usage:"Force overwrite without confirmation"`
	Verbose bool     `cli:"v,verbose" usage:"Enable verbose output"`
	Tags    []string `cli:"diff,check,dry-run" usage:"Dry run mode"`
	Files   []string `cli:",positional"`
}

func TestHelp_RoutingAndFlagDiscovery(t *testing.T) {
	t.Parallel()

	testAct := action.New("user.create", func(ctx context.Context, req HelpSampleReq) (string, error) {
		return "ok", nil
	}).
		Description("Create a new user account").
		Route(
			tcli.Command("create", "Create a new user account").
				WithAliases("c", "new").
				WithExamples(
					"mytool create --name Alice --force",
					"mytool create -n Bob -f",
				),
		).
		Build()

	t.Run("Subcommand Help dynamic binary name and all aliases", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		cli := tcli.New(
			tcli.WithArgs("create", "--help"),
			tcli.WithExecutable("mytool"),
			tcli.WithOutput(&stdout, &stderr),
		)
		cli.Mount([]action.AnyAction{testAct})

		_, err := cli.Do(context.Background(), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		out := stderr.String()
		if !strings.Contains(out, "Usage:\n  mytool create [flags] [files...]") {
			t.Errorf("expected positional args in usage, got:\n%s", out)
		}
		if !strings.Contains(out, "-n, --name <string>") || !strings.Contains(out, "User full name") {
			t.Errorf("missing reflected flags in help output:\n%s", out)
		}
		if !strings.Contains(out, "--diff, --check, --dry-run <paths...>") {
			t.Errorf("missing multi-alias flag rendering:\n%s", out)
		}
	})

	t.Run("Help with unknown topic returns error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		cli := tcli.New(
			tcli.WithArgs("help", "unknown-topic"),
			tcli.WithOutput(&stdout, &stderr),
		)
		cli.Mount([]action.AnyAction{testAct})

		_, err := cli.Do(context.Background(), nil)
		if err == nil {
			t.Fatal("expected error for unknown help topic")
		}
		if !strings.Contains(stderr.String(), `unknown command "unknown-topic"`) {
			t.Fatalf("expected unknown topic error, got:\n%s", stderr.String())
		}
	})

	t.Run("Help with unknown topic returns error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		cli := tcli.New(
			tcli.WithArgs("help", "unknown-topic"),
			tcli.WithOutput(&stdout, &stderr),
		)
		cli.Mount([]action.AnyAction{testAct})

		_, err := cli.Do(context.Background(), nil)
		if err == nil {
			t.Fatal("expected error for unknown help topic")
		}
		if !strings.Contains(stderr.String(), `unknown command "unknown-topic"`) {
			t.Fatalf("expected unknown topic error, got:\n%s", stderr.String())
		}
	})
}
