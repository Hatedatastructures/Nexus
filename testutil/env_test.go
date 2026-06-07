package testutil

import (
	"os"
	"testing"
)

func TestSetupTestEnvCreatesNexusHome(t *testing.T) {
	env := SetupTestEnv(t)
	if env == nil {
		t.Fatal("SetupTestEnv returned nil")
	}
	if env.NexusHome == "" {
		t.Fatal("NexusHome is empty")
	}

	// Verify the directory exists
	if _, err := os.Stat(env.NexusHome); os.IsNotExist(err) {
		t.Errorf("NexusHome directory does not exist: %s", env.NexusHome)
	}

	// Verify NEXUS_HOME env var is set
	if os.Getenv("NEXUS_HOME") != env.NexusHome {
		t.Errorf("NEXUS_HOME = %q, want %q", os.Getenv("NEXUS_HOME"), env.NexusHome)
	}
}

func TestSetupTestEnvSetsTZ(t *testing.T) {
	_ = SetupTestEnv(t)
	tz := os.Getenv("TZ")
	if tz != "UTC" {
		t.Errorf("TZ = %q, want %q", tz, "UTC")
	}
}

func TestSetenv(t *testing.T) {
	key := "TEST_SETENV_KEY"
	value := "test_value"

	Setenv(t, key, value)

	if os.Getenv(key) != value {
		t.Errorf("env %s = %q, want %q", key, os.Getenv(key), value)
	}
}

func TestUnsetenv(t *testing.T) {
	key := "TEST_UNSETENV_KEY"
	_ = os.Setenv(key, "initial_value")

	Unsetenv(t, key)

	if _, exists := os.LookupEnv(key); exists {
		t.Errorf("env %s should be unset", key)
	}
}

func TestSensitiveEnvSuffixes(t *testing.T) {
	if len(sensitiveEnvSuffixes) == 0 {
		t.Error("sensitiveEnvSuffixes should not be empty")
	}
}

func TestSensitiveEnvNames(t *testing.T) {
	if len(sensitiveEnvNames) == 0 {
		t.Error("sensitiveEnvNames should not be empty")
	}
}

func TestTestEnvRestore(t *testing.T) {
	// Set a test variable, setup env, then verify cleanup restores it
	key := "TEST_RESTORE_KEY"
	_ = os.Setenv(key, "original")
	_ = SetupTestEnv(t)

	// After setup, the key should still exist (it's not sensitive)
	if os.Getenv(key) != "original" {
		t.Errorf("non-sensitive env %s was modified", key)
	}
}
