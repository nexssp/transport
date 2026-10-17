// Package flowutil contains shared helpers for Nexss Flow adapters.
package flowutil

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nexssp/flow/compiler/contracts"
	"github.com/nexssp/kernel/action"
)

// ResolveActions accepts an explicit library or action slice, then falls back
// to the action resolver available in a Flow execution context.
func ResolveActions(ctx context.Context, workload any) (actions []action.AnyAction, ok bool) {
	if lib, isLibrary := workload.(action.Library); isLibrary {
		return lib.Actions, true
	}
	if acts, isActions := workload.([]action.AnyAction); isActions {
		return acts, true
	}

	resolver := contracts.ActionResolverFromContext(ctx)
	if resolver == nil {
		return nil, false
	}
	lister, isLister := resolver.(interface{ Actions() []action.AnyAction })
	if !isLister {
		return nil, false
	}
	return lister.Actions(), true
}

// Decode returns a Kernel decoder that converts a Flow value (usually a map)
// into the concrete request type expected by an action.
func Decode(value any) action.DecodeFunc {
	return func(target any) error {
		if value == nil {
			return nil
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode Flow payload: %w", err)
		}
		if err := json.Unmarshal(encoded, target); err != nil {
			return fmt.Errorf("decode Flow payload: %w", err)
		}
		return nil
	}
}

// DecodeInto converts a Flow input into a typed adapter request.
func DecodeInto(value, target any) error {
	return Decode(value)(target)
}
