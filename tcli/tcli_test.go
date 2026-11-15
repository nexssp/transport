package tcli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transport/tcli"
)

type PackReq struct {
	Profile string   `cli:"profile,p"`
	Compact bool     `cli:"compact,c"`
	Files   []string `cli:",positional"`
}

type PackRes struct {
	Status string   `json:"status"`
	Files  []string `json:"files"`
}

func TestTCLI_CommandRoutingAndFlagBinding(t *testing.T) {
	t.Parallel()

	packAct := action.New("srcpack.pack", func(_ context.Context, req PackReq) (PackRes, error) {
		return PackRes{
			Status: "packed:" + req.Profile,
			Files:  req.Files,
		}, nil
	}).Route(tcli.Command("pack", "Package code").WithAliases("p")).Build()

	var stdout, stderr bytes.Buffer
	cli := tcli.New(
		tcli.WithArgs("pack", "-c", "--profile=arch", "main.go", "config.go"),
		tcli.WithOutput(&stdout, &stderr),
	)

	cli.Mount([]action.AnyAction{packAct})

	_, err := cli.Do(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `"status": "packed:arch"`) {
		t.Fatalf("unexpected status:\n%s", out)
	}
	if !strings.Contains(out, `"main.go"`) || !strings.Contains(out, `"config.go"`) {
		t.Fatalf("positional files were not captured:\n%s", out)
	}
}

func TestTCLI_RootRoutePreservesFlagsAndPositionals(t *testing.T) {
	t.Parallel()

	type RepoPeekRequest struct {
		View1 bool     `cli:"view1"`
		File  string   `cli:"f,file"`
		Repos []string `cli:",positional"`
	}
	var captured RepoPeekRequest
	root := action.New("repopeek.root", func(_ context.Context, req RepoPeekRequest) (string, error) {
		captured = req
		return "ok", nil
	}).Route(tcli.RootCommand("Inspect repositories")).Build()

	var stdout, stderr bytes.Buffer
	cli := tcli.New(
		tcli.WithArgs("--view1", "-f", "repos.txt", "github.com/nexssp/repo", "owner/other"),
		tcli.WithOutput(&stdout, &stderr),
		tcli.WithExecutable("repopeek"),
	)
	cli.Mount([]action.AnyAction{root})

	if _, err := cli.Do(context.Background(), nil); err != nil {
		t.Fatalf("Do() error = %v; stderr: %s", err, stderr.String())
	}
	if !captured.View1 || captured.File != "repos.txt" {
		t.Fatalf("captured flags = %+v, want view1 and file", captured)
	}
	wantRepos := []string{"github.com/nexssp/repo", "owner/other"}
	if len(captured.Repos) != len(wantRepos) || captured.Repos[0] != wantRepos[0] || captured.Repos[1] != wantRepos[1] {
		t.Fatalf("captured repos = %#v, want %#v", captured.Repos, wantRepos)
	}
	if got := strings.TrimSpace(stdout.String()); got != "ok" {
		t.Fatalf("stdout = %q, want %q", got, "ok")
	}
}

func TestTCLI_NamedCommandTakesPrecedenceOverRootRoute(t *testing.T) {
	t.Parallel()

	root := action.New("app.root", func(context.Context, struct{}) (string, error) {
		return "root", nil
	}).Route(tcli.RootCommand("Default route")).Build()
	named := action.New("app.named", func(context.Context, struct{}) (string, error) {
		return "named", nil
	}).Route(tcli.Command("run", "Run command")).Build()

	var stdout, stderr bytes.Buffer
	cli := tcli.New(tcli.WithArgs("run"), tcli.WithOutput(&stdout, &stderr))
	cli.Mount([]action.AnyAction{root, named})
	if _, err := cli.Do(context.Background(), nil); err != nil {
		t.Fatalf("Do() error = %v; stderr: %s", err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "named" {
		t.Fatalf("stdout = %q, want named command result", got)
	}
}

func TestTCLI_NilOptionSafety(t *testing.T) {
	t.Parallel()

	// Should not panic when nil option is passed
	cli := tcli.New(nil)
	if cli == nil {
		t.Fatal("expected non-nil cli instance")
	}
}
