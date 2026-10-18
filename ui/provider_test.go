package ui

import (
	"strings"
	"testing"
)

// The report: the interface said "this build cannot list models", which blames
// a packaging fault that does not exist.
//
// The cause is that the interface was started without --provider openrouter, so
// the fixture provider is in charge and there is no catalogue. The message has
// to say that, and give the command, or the user is left hunting for a bug in
// their installation.
func TestModelsWithoutACatalogueNamesTheRealCause(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.info.Provider = "fixture"
	m.runCommand("/models")

	text := joined(m)
	if strings.Contains(text, "this build cannot list models") {
		t.Fatalf("the message still blames the build:\n%s", text)
	}
	if !strings.Contains(text, "--provider openrouter") {
		t.Fatalf("the message gives no command:\n%s", text)
	}
	if !strings.Contains(text, "fixture") {
		t.Fatalf("the message does not name the provider in charge:\n%s", text)
	}
}

// A provider that has a catalogue configured but cannot reach one is a
// different fault and must not be described the same way.
func TestModelsWithoutACatalogueForAHostedProviderIsDistinct(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.info.Provider = "openrouter" // browser left nil
	m.runCommand("/models")

	text := joined(m)
	if strings.Contains(text, "fixture") {
		t.Fatalf("a hosted provider was blamed on the fixture:\n%s", text)
	}
	if !strings.Contains(text, "openrouter") {
		t.Fatalf("the provider is not named:\n%s", text)
	}
}

// The footer showed a hosted model name while the fixture was answering, which
// made the screen read as though that model was in use.
func TestFooterDoesNotClaimAModelUnderTheFixture(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.info.Provider = "fixture"
	m.info.Model = "z-ai/glm-5.3" // configured, but the fixture never uses it

	footer := m.footer(80)
	if strings.Contains(footer, "glm-5.3") {
		t.Fatalf("the footer shows a model the fixture never uses:\n%s", footer)
	}
	if !strings.Contains(footer, "offline") {
		t.Fatalf("the footer does not say the provider is offline:\n%s", footer)
	}
	if strings.Contains(footer, "unverified") {
		t.Fatalf("the footer implies a connection is pending for a provider that needs none:\n%s", footer)
	}
}

// With a hosted provider the footer must be unchanged: the model in force is
// the one worth showing.
func TestFooterStillShowsTheModelForAHostedProvider(t *testing.T) {
	m := newCmdModel(&fakeSettings{})
	m.info.Provider = "openrouter"
	m.info.Model = "z-ai/glm-5.3"
	m.verified = true

	footer := m.footer(80)
	if !strings.Contains(footer, "glm-5.3") {
		t.Fatalf("the footer dropped the model in force:\n%s", footer)
	}
	if !strings.Contains(footer, "connected") {
		t.Fatalf("the footer does not report the connection:\n%s", footer)
	}
}
