package gqlcli

import (
	"os"
	"testing"
)

// TestMain keeps the CLI's on-disk schema cache out of tests: they would
// otherwise write entries for every httptest server into the user cache dir.
func TestMain(m *testing.M) {
	os.Setenv("GQLCLI_SCHEMA_CACHE_TTL", "0")
	os.Exit(m.Run())
}
