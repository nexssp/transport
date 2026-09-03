package tcli_test

import (
	"testing"

	"github.com/nexssp/transport/tcli"
)

func TestCLIBinding_CreationAndMethods(t *testing.T) {
	t.Parallel()

	binding := tcli.Command("pack", "Package source code").
		WithAliases("p", "bundle").
		WithExamples("srcpack pack -c", "srcpack pack -t backend")

	if binding.Command != "pack" {
		t.Fatalf("expected Command 'pack', got %q", binding.Command)
	}
	if len(binding.Aliases) != 2 || binding.Aliases[0] != "p" || binding.Aliases[1] != "bundle" {
		t.Fatalf("unexpected aliases: %v", binding.Aliases)
	}
	if len(binding.Examples) != 2 || binding.Examples[0] != "srcpack pack -c" {
		t.Fatalf("unexpected examples: %v", binding.Examples)
	}
	if binding.String() != "cli: pack" {
		t.Fatalf("expected String() 'cli: pack', got %q", binding.String())
	}

	tr := tcli.New()
	if !tr.CanHandle(binding) {
		t.Fatal("expected CanHandle(CLIBinding) to return true")
	}
	if tr.CanHandle("http: /api/v1/pack") {
		t.Fatal("expected CanHandle(string) to return false for non-CLIBinding")
	}
}
