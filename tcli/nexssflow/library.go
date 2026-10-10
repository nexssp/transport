package nexssflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nexssp/flow/contracts"
	"github.com/nexssp/flow/core"
	flowschema "github.com/nexssp/flow/extensions/schema"
	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transport/tcli"
)

type Config struct {
	Executable string `nflow:"executable"`
}

func init() {
	core.Register("tcli", Bundle)
}

func Bundle(opts map[string]string) core.Bundle {
	cfg, err := core.Decode[Config](opts)
	if err != nil {
		panic(err)
	}
	return core.Bundle{
		ID:        "tcli",
		Libraries: []action.Library{Library(cfg)},
		Modifiers: []core.Modifier{
			core.String("cli", func(b *action.Builder[any, any], raw string) *action.Builder[any, any] {
				cmd, desc, _ := strings.Cut(raw, ":")
				return b.Route(tcli.Command(strings.TrimSpace(cmd), strings.TrimSpace(desc)))
			}),
		},
		WrapPipeline: wrapPipelineRequestSchemas,
	}
}

type requestSchemasContextKey struct{}

func wrapPipelineRequestSchemas(meta map[string]any, inner action.AnyAction) (action.AnyAction, error) {
	requestSchemas, err := requestSchemasFromMeta(meta)
	if err != nil {
		return nil, err
	}
	if len(requestSchemas) == 0 {
		return inner, nil
	}

	return action.Dynamic(inner).Use(func(next action.Fn[any, any]) action.Fn[any, any] {
		return func(ctx context.Context, req any) (any, error) {
			ctx = context.WithValue(ctx, requestSchemasContextKey{}, requestSchemas)
			return next(ctx, req)
		}
	}).Build(), nil
}

func requestSchemasFromMeta(meta map[string]any) (map[string]tcli.CLIRequestSchema, error) {
	schemas := make(map[string]flowschema.Schema)
	for _, schema := range flowschema.SchemasFromMap(meta) {
		schemas[schema.Name] = schema
	}
	pipelineModifiers, _ := meta["pipeline_modifiers"].(map[string][]string)
	requestSchemas := make(map[string]tcli.CLIRequestSchema)
	for pipelineName, modifiers := range pipelineModifiers {
		hasCLI, schemaName := false, ""
		for _, raw := range modifiers {
			key, value, hasValue := strings.Cut(raw, "=")
			if !hasValue {
				continue
			}
			switch strings.TrimSpace(key) {
			case "cli":
				hasCLI = true
			case "schema":
				schemaName = strings.TrimSpace(value)
			}
		}
		if !hasCLI || schemaName == "" {
			continue
		}

		schema, ok := schemas[schemaName]
		if !ok {
			return nil, fmt.Errorf("tcli: pipeline %q references undeclared @schema %q", pipelineName, schemaName)
		}
		requestSchema, err := CLIRequestSchemaFromFlowSchema(schema)
		if err != nil {
			return nil, fmt.Errorf("tcli: pipeline %q: %w", pipelineName, err)
		}
		canonicalName := pipelineName
		if !strings.Contains(canonicalName, ".") {
			canonicalName = "pipeline." + canonicalName
		}
		requestSchemas[canonicalName] = requestSchema
	}
	return requestSchemas, nil
}

// CLIRequestSchemaFromFlowSchema converts a Flow @schema declaration
// into the tcli request schema consumed by ParseCLIRequest.
func CLIRequestSchemaFromFlowSchema(schema flowschema.Schema) (tcli.CLIRequestSchema, error) {
	requestSchema := tcli.CLIRequestSchema{Name: schema.Name}
	for _, field := range schema.Fields {
		cliTag := strings.TrimSpace(field.Tags["cli"])
		if cliTag == "" {
			continue
		}
		var flags []string
		positional := false
		for part := range strings.SplitSeq(cliTag, ",") {
			part = strings.TrimSpace(part)
			switch part {
			case "positional", "args":
				positional = true
			case "":
			default:
				flags = append(flags, part)
			}
		}
		requestSchema.Fields = append(requestSchema.Fields, tcli.CLIFieldSpec{
			Name:       field.JSONName,
			Type:       field.Type,
			Flags:      flags,
			Usage:      field.Tags["usage"],
			Required:   hasTag(field.Tags["validate"], "required"),
			Positional: positional,
		})
	}
	if err := tcli.ValidateRequestSchema(requestSchema); err != nil {
		return tcli.CLIRequestSchema{}, err
	}
	return requestSchema, nil
}

func hasTag(raw, name string) bool {
	for part := range strings.SplitSeq(raw, ",") {
		if strings.TrimSpace(part) == name {
			return true
		}
	}
	return false
}

func requestSchemasFromContext(ctx context.Context) map[string]tcli.CLIRequestSchema {
	requestSchemas, _ := ctx.Value(requestSchemasContextKey{}).(map[string]tcli.CLIRequestSchema)
	return requestSchemas
}

func Library(cfg Config) action.Library {
	opts := []tcli.Option{}
	if cfg.Executable != "" {
		opts = append(opts, tcli.WithExecutable(cfg.Executable))
	}
	return LibraryWithTransport(tcli.New(opts...))
}

// RoutingOnlyBundle returns a bundle that registers :cli= as a no-op
// modifier.
func RoutingOnlyBundle() core.Bundle {
	return core.Bundle{
		ID:        "tcli.routing",
		Libraries: []action.Library{{Name: "tcli.routing"}},
		Modifiers: []core.Modifier{
			core.String("cli", func(b *action.Builder[any, any], _ string) *action.Builder[any, any] {
				return b
			}),
		},
	}
}

// LibraryWithTransport returns the Flow listener library using a
// caller-built transport.
func LibraryWithTransport(tr *tcli.Transport) action.Library {
	if tr == nil {
		tr = tcli.New()
	}
	return action.Library{
		Name: "transport.tcli",
		Actions: []action.AnyAction{
			listenTriggerAction(tr),
		},
	}
}

type tcliWorkload struct {
	actions        []action.AnyAction
	autoDiscovered bool
}

func resolveTCLIWorkload(ctx context.Context, workload any) (tcliWorkload, error) {
	switch w := workload.(type) {
	case action.Library:
		return tcliWorkload{actions: w.Actions}, nil
	case []action.AnyAction:
		return tcliWorkload{actions: w}, nil
	}

	resolver := contracts.ActionResolverFromContext(ctx)
	if resolver == nil {
		return tcliWorkload{}, errors.New("tcli.listen: action resolver is nil in execution context")
	}
	lister, ok := resolver.(interface{ Actions() []action.AnyAction })
	if !ok {
		return tcliWorkload{}, fmt.Errorf("tcli.listen: resolver (%T) does not implement Actions()", resolver)
	}

	var discovered []action.AnyAction
	allActions := lister.Actions()
	for _, act := range allActions {
		if act == nil {
			continue
		}
		for _, binding := range act.GetBindings() {
			if _, isCLI := binding.(tcli.CLIBinding); isCLI {
				discovered = append(discovered, act)
				break
			}
		}
	}
	if len(discovered) == 0 {
		var names []string
		for _, act := range allActions {
			if act != nil && act.Describe() != nil {
				names = append(names, fmt.Sprintf("%s (bindings=%v)", act.Describe().Name, act.GetBindings()))
			}
		}
		return tcliWorkload{}, fmt.Errorf("tcli.listen: auto-discovery found 0 CLI-bound actions. Available in resolver: %v", names)
	}
	return tcliWorkload{actions: discovered, autoDiscovered: true}, nil
}

func listenTriggerAction(tr *tcli.Transport) action.AnyAction {
	return action.New("tcli.listen", func(ctx context.Context, workload any) (any, error) {
		resolved, err := resolveTCLIWorkload(ctx, workload)
		if err != nil {
			return nil, err
		}
		actionsToMount := resolved.actions

		if resolved.autoDiscovered {
			requestSchemas := requestSchemasFromContext(ctx)
			for index, act := range actionsToMount {
				if act == nil || act.Describe() == nil {
					continue
				}
				if requestSchema, ok := requestSchemas[act.Describe().Name]; ok {
					actionsToMount[index] = action.Dynamic(act).Route(tcli.CLIInputBinding{Schema: requestSchema}).Build()
				}
			}
		}

		tr.Mount(actionsToMount)
		return tr.Do(ctx, nil)
	}).
		Tag("transport", "tcli").
		Build()
}
