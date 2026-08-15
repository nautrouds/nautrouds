package options

import (
	"flag"
	"testing"
)

func resetFlags() {
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
}

func TestEnvBool_FallbackWhenUnset(t *testing.T) {
	resetFlags()
	t.Setenv("NAUTROUDS_TEST_FLAG_UNSET", "")
	t.Setenv("NAUTROUDS_TEST_FLAG_UNSET_MARKER", "")
	ptr := EnvBool("test-flag-unset", "NAUTROUDS_TEST_FLAG_UNSET_DOES_NOT_EXIST", true, "usage")
	if !*ptr {
		t.Errorf("expected fallback true, got %v", *ptr)
	}
}

func TestEnvBool_ReadsEnv(t *testing.T) {
	resetFlags()
	t.Setenv("NAUTROUDS_TEST_FLAG_ENV", "false")
	ptr := EnvBool("test-flag-env", "NAUTROUDS_TEST_FLAG_ENV", true, "usage")
	if *ptr {
		t.Errorf("expected env override false, got %v", *ptr)
	}
}

func TestEnvBool_InvalidEnvFallsBack(t *testing.T) {
	resetFlags()
	t.Setenv("NAUTROUDS_TEST_FLAG_INVALID", "not-a-bool")
	ptr := EnvBool("test-flag-invalid", "NAUTROUDS_TEST_FLAG_INVALID", true, "usage")
	if !*ptr {
		t.Errorf("expected fallback true on invalid env, got %v", *ptr)
	}
}
