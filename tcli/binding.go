package tcli

// CLIBinding maps an action to a command name, documentation, and examples.
type CLIBinding struct {
	Command     string
	Aliases     []string
	Description string
	Examples    []string
}

func (b CLIBinding) String() string {
	return "cli: " + b.Command
}

// Command constructs a new CLI route binding.
func Command(cmd, desc string) CLIBinding {
	return CLIBinding{Command: cmd, Description: desc}
}

// WithAliases attaches alternative invocation aliases.
func (b CLIBinding) WithAliases(aliases ...string) CLIBinding {
	b.Aliases = append(b.Aliases, aliases...)
	return b
}

// WithExamples attaches usage examples to the command help output.
func (b CLIBinding) WithExamples(examples ...string) CLIBinding {
	b.Examples = append(b.Examples, examples...)
	return b
}

// CLIFieldSpec describes one request field exposed as a CLI flag or positional.
type CLIFieldSpec struct {
	Name       string
	Type       string
	Flags      []string
	Usage      string
	Required   bool
	Positional bool
}

// CLIRequestSchema describes the dynamic request shape for a CLI command.
type CLIRequestSchema struct {
	Name   string
	Fields []CLIFieldSpec
}

// CLIInputBinding attaches a CLI request schema to an action independently
// of the command routing binding.
type CLIInputBinding struct {
	Schema CLIRequestSchema
}

func (b CLIInputBinding) String() string {
	return "cli-schema: " + b.Schema.Name
}
