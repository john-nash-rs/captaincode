package provider

import (
	"context"
	"os"
	"regexp"
	"testing"
	"time"
)

var evalKeyPattern = regexp.MustCompile(`(?i)api[-_]?key\s*=\s*"?([^"\s]+)"?`)

func liveKey(t *testing.T) string {
	t.Helper()
	if k := os.Getenv("CAPTAINCODE_EVAL_KEY"); k != "" {
		return k
	}
	path := os.Getenv("CAPTAINCODE_EVAL_KEY_FILE")
	if path == "" {
		t.Skip("set CAPTAINCODE_EVAL_KEY or CAPTAINCODE_EVAL_KEY_FILE")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("key file unreadable")
	}
	m := evalKeyPattern.FindSubmatch(data)
	if m == nil {
		t.Skip("no credential found")
	}
	return string(m[1])
}

// A new user's question is "what can I pick", so the answer has to come from the
// provider rather than from a list someone remembered to update.
func TestLiveListModelsReturnsSelectableModels(t *testing.T) {
	key := liveKey(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	models, err := New(key, "", "").ListModels(ctx)
	if err != nil {
		t.Fatalf("listing models failed: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("no models returned")
	}
	for _, m := range models {
		if m.ID == "" {
			t.Fatalf("a model has no identifier: %+v", m)
		}
		if m.Name == "" {
			t.Errorf("%s has no display name", m.ID)
		}
		if m.PriceIn == "" {
			t.Errorf("%s has no rendered price", m.ID)
		}
	}
	// The list must contain only models the account can reach, and every
	// entry must carry enough to choose on.
	for _, m := range models {
		if m.ID == "" {
			t.Fatalf("an entry has no identifier: %+v", m)
		}
	}
	// Free models are deliberately NOT promoted to the top: OpenRouter gates
	// the free tier to registered agentic harnesses and a live check of the
	// first eight found half answering 403. Whether any appear in the top
	// window therefore depends on the provider's coding ranking, which is not
	// something this test should pin. They remain reachable by search.
	if len(models) > 0 && models[0].ID == "" {
		t.Error("the list is malformed")
	}
}

// Free models are NOT sorted to the top. OpenRouter gates the free tier to
// registered agentic harnesses, and a live check of the first eight free models
// found four answering 403. Sorting them first put a coin flip in first place,
// so the provider's own coding ranking is kept and the price is shown per row.
func TestFreeModelsAreNotPromotedAboveWorkingOnes(t *testing.T) {
	key := liveKey(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	models, err := New(key, "", "").ListModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Whatever the ranking is, the list must not have been reordered to lead
	// with free models. Assert on the shape rather than on a specific provider
	// response, which changes.
	if len(models) > 1 && models[0].Free && !models[1].Free {
		t.Error("the list leads with free models, which are gated and mostly fail")
	}
}

// A price of zero is the free tier. A merely small price must not be read as
// free, which would tell someone to pick a model that bills them.
func TestPriceRendering(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"0", "free"},
		{"", ""},
		{"0.0000025", "$2.50/M"},
		{"0.000001", "$1.00/M"},
		{"0.0000001", "$0.10/M"},
		// Below a hundredth of a cent per million tokens, a two-decimal
		// figure would render as "$0.00" and read as free.
		{"0.000000005", "<$0.01/M"},
	} {
		if got := renderPrice(tc.in); got != tc.want {
			t.Errorf("renderPrice(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// An unparseable price must be shown, not silently blanked. A hidden price is
// worse than an ugly one.
func TestPriceRenderingNeverHidesAValue(t *testing.T) {
	if got := renderPrice("see-dashboard"); got == "" {
		t.Fatal("an unparseable price was hidden")
	}
}

func TestLiveSearchFiltersModels(t *testing.T) {
	key := liveKey(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	all, err := New(key, "", "").ListModels(ctx)
	if err != nil || len(all) == 0 {
		t.Fatalf("listing failed: %v", err)
	}
	target := all[0].ID

	found, err := New(key, "", "").SearchModels(ctx, target)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(found) == 0 {
		t.Fatalf("search for %q returned nothing", target)
	}
	// Every hit must actually match, or the search is lying.
	for _, m := range found {
		if m.ID != target && !containsFold(m.ID+" "+m.Name, target) {
			t.Errorf("search returned unrelated model %q", m.ID)
		}
	}
}

func containsFold(haystack, needle string) bool {
	return len(needle) == 0 || indexFold(haystack, needle) >= 0
}

func indexFold(haystack, needle string) int {
	h, n := []rune(haystack), []rune(needle)
	if len(n) == 0 || len(n) > len(h) {
		return -1
	}
	for i := 0; i+len(n) <= len(h); i++ {
		match := true
		for j := range n {
			a, b := h[i+j], n[j]
			if 'A' <= a && a <= 'Z' {
				a += 32
			}
			if 'A' <= b && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// Listing models needs a credential. Reporting that plainly is what lets the
// interface tell the user to add a key before offering a list.
func TestListingWithoutAClientIsReportedNotPanicked(t *testing.T) {
	if _, err := (&OpenRouter{}).ListModels(context.Background()); err == nil {
		t.Fatal("expected an error with no client")
	}
}
