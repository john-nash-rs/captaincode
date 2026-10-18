package main

// resolveProvider decides which provider a run uses when --provider was not
// given.
//
// Defaulting to the fixture unconditionally was wrong twice over: someone who
// had configured a key and a model was told they had no model and no write
// tools, because the offline provider was in charge and it has neither. The
// offline provider is now the fallback for a machine with nothing configured,
// which is the only case where it is what was wanted.
//
// Both a model and a credential are required. Either alone is a half setup
// that cannot work, and choosing the offline provider there reproduces the same
// confusing symptom as having configured nothing at all.
func resolveProvider(flagValue, model, credential string, credentialErr error) string {
	if flagValue != "" {
		return flagValue
	}
	if model != "" && credentialErr == nil && credential != "" {
		return "openrouter"
	}
	return "fixture"
}
