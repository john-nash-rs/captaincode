package main

import (
	"context"

	"github.com/john-nash-rs/captaincode/provider"
	"github.com/john-nash-rs/captaincode/ui"
)

// openrouterBrowser adapts the provider's model catalogue to the interface,
// so choosing a model means picking from a list rather than recalling a slug.
type openrouterBrowser struct {
	adapter *provider.OpenRouter
}

func (b *openrouterBrowser) List(ctx context.Context) ([]ui.Model, error) {
	models, err := b.adapter.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	return toUIModels(models), nil
}

func (b *openrouterBrowser) Search(ctx context.Context, query string) ([]ui.Model, error) {
	models, err := b.adapter.SearchModels(ctx, query)
	if err != nil {
		return nil, err
	}
	return toUIModels(models), nil
}

func toUIModels(models []provider.ModelInfo) []ui.Model {
	out := make([]ui.Model, 0, len(models))
	for _, m := range models {
		// The prompt price is the one that matters for picking, and the free
		// badge already says "free", so showing one figure keeps the row short
		// enough to read on a normal terminal.
		price := m.PriceIn
		if m.Free {
			price = "free"
		}
		out = append(out, ui.Model{ID: m.ID, Name: m.Name, Price: price, Context: m.Context, Free: m.Free})
	}
	return out
}

// openrouterProbe adapts the provider's connectivity check to the interface.
//
// The indirection keeps the network code in the provider package, where the
// endpoint and the error classification already live, so the interface never
// learns anything about how a check is actually performed.
type openrouterProbe struct {
	adapter *provider.OpenRouter
}

// Check reports one result per model. Results carry only an error value, never
// a credential, so a probe outcome is safe to render.
func (p *openrouterProbe) Check(ctx context.Context, models []string) []ui.Result {
	raw := p.adapter.Check(ctx, models)
	results := make([]ui.Result, 0, len(raw))
	for _, r := range raw {
		results = append(results, ui.Result{Model: r.Model, Err: r.Err})
	}
	return results
}
