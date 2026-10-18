package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/OpenRouterTeam/go-sdk/models/operations"
)

// ModelInfo is one selectable model, with enough detail to choose between them
// without leaving the terminal.
type ModelInfo struct {
	ID       string
	Name     string
	Context  int64
	PriceIn  string // prompt price, already rendered for display
	PriceOut string
	Free     bool
}

// modelLimit is how many models a screen offers. The provider sorts by coding
// ability, so the first entries are the ones worth showing; more than this is
// noise nobody scrolls through.
const modelLimit = 40

// catalogLimit is how far into the provider's ranking to read.
//
// It has to exceed modelLimit by a wide margin. The catalogue holds several
// hundred models of which only a handful are free, and the free ones sort
// below paid ones, so reading only the first forty surfaced none of them and
// left a new user with nothing to try before spending anything.
const catalogLimit = 250

// ListModels returns the models the account may use, best for coding first.
//
// Hardcoding a short list was wrong: it goes stale, and it cannot answer the
// only question a new user actually has, which is "what can I pick". Asking the
// provider means the list is current and specific to the account's access.
func (p *OpenRouter) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return p.fetchModels(ctx, nil)
}

// SearchModels filters the account's models by name, which the provider does
// server-side rather than by downloading everything and matching here.
func (p *OpenRouter) SearchModels(ctx context.Context, query string) ([]ModelInfo, error) {
	if query == "" {
		return p.fetchModels(ctx, nil)
	}
	text := query
	return p.fetchModels(ctx, &operations.GetModelsRequest{Q: &text})
}

func (p *OpenRouter) fetchModels(ctx context.Context, extra *operations.GetModelsRequest) ([]ModelInfo, error) {
	if p == nil || p.Client == nil || p.Client.Models == nil {
		return nil, ErrNoCredential
	}
	limit := int64(catalogLimit)
	byCoding := operations.GetModelsSortCodingHighToLow
	request := &operations.GetModelsRequest{Limit: &limit, Sort: &byCoding}
	if extra != nil {
		request.Q = extra.Q
	}

	response, err := p.Client.Models.List(ctx, request)
	if err != nil {
		return nil, classify(err)
	}

	out := make([]ModelInfo, 0, len(response.Result.Data))
	for _, m := range response.Result.Data {
		// Batch variants bill at a discount but answer asynchronously, which
		// an interactive agent cannot use. Offering them would be offering
		// something that appears to work and then never replies.
		if strings.Contains(m.ID, ":batch") {
			continue
		}
		info := ModelInfo{
			ID:       m.ID,
			Name:     m.Name,
			PriceIn:  renderPrice(m.Pricing.Prompt),
			PriceOut: renderPrice(m.Pricing.Completion),
			// A price of exactly zero is what marks the free tier. Comparing
			// the rendered string would misclassify a merely cheap model.
			Free: m.Pricing.Prompt == "0" && m.Pricing.Completion == "0",
		}
		if m.ContextLength != nil {
			info.Context = *m.ContextLength
		}
		if info.Name == "" {
			info.Name = info.ID
		}
		out = append(out, info)
	}

	// The provider's own coding ranking is left alone.
	//
	// An earlier version put free models first, on the reasoning that someone
	// setting this up is trying it before spending anything. A live check of
	// the first eight free models found that four of them answer 403: OpenRouter
	// gates the free tier to registered agentic harnesses and CaptainCode is not
	// one. Sorting them to the top therefore put a coin flip in first place.
	// Cost is shown on each row instead, so the trade is visible rather than
	// pre-decided.
	if len(out) > modelLimit {
		out = out[:modelLimit]
	}
	return out, nil
}

// renderPrice shows a per-token price the way it is actually reasoned about.
//
// OpenRouter quotes dollars per token, so a raw "0.0000025" is unreadable. The
// useful figure is dollars per million tokens, which is the unit providers
// advertise in.
func renderPrice(perToken string) string {
	if perToken == "" {
		return ""
	}
	if perToken == "0" {
		return "free"
	}
	value, err := strconv.ParseFloat(perToken, 64)
	if err != nil {
		return perToken // show it verbatim rather than hide a price
	}
	perMillion := value * 1_000_000
	if perMillion < 0.01 {
		// Anything this small rounds to a misleading "$0.00".
		return "<$0.01/M"
	}
	return fmt.Sprintf("$%.2f/M", perMillion)
}
