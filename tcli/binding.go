package tcli

// CLIBinding maps an action to a command name and documentation.
type CLIBinding struct {
	Command     string
	Aliases     []string
	Description string
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
