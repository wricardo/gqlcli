package main

import "testing"

func TestCLIVersionUsesBuildVersion(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })

	Version = "v9.9.9"
	if got := cliVersion(); got != "v9.9.9" {
		t.Fatalf("cliVersion() = %q, want %q", got, "v9.9.9")
	}
}

func TestCLIVersionFallsBackToDevWhenEmpty(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })

	Version = " "
	if got := cliVersion(); got != "dev" {
		t.Fatalf("cliVersion() = %q, want %q", got, "dev")
	}
}
