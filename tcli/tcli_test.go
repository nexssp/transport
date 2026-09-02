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

	packAct := action.New("srcpack.pack", func(ctx context.Context, req PackReq) (PackRes, error) {
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

func TestTCLI_NilOptionSafety(t *testing.T) {
	t.Parallel()

	// Should not panic when nil option is passed
	cli := tcli.New(nil, nil)
	if cli == nil {
		t.Fatal("expected non-nil cli instance")
	}
}
