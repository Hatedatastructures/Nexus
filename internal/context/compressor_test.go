package context

import (
	"testing"
)

// TestNewCompressor_Defaults verifies that NewCompressor applies the expected
// default values when given reasonable positive inputs.
func TestNewCompressor_Defaults(t *testing.T) {
	t.Parallel()

	c := NewCompressor(3, 20000)

	if c.protectFirstN != 3 {
		t.Errorf("protectFirstN: got %d, want 3", c.protectFirstN)
	}
	if c.tailTokenBudget != 20000 {
		t.Errorf("tailTokenBudget: got %d, want 20000", c.tailTokenBudget)
	}
	if c.thresholdPercent != 0.75 {
		t.Errorf("thresholdPercent: got %f, want 0.75", c.thresholdPercent)
	}
}

// TestCompressor_SetThresholdPercent verifies the SetThresholdPercent method
// accepts valid values and silently ignores invalid ones.
func TestCompressor_SetThresholdPercent(t *testing.T) {
	t.Parallel()

	c := NewCompressor(3, 20000)

	// Valid values should be accepted.
	for _, pct := range []float64{0.1, 0.5, 0.75, 1.0} {
		c.SetThresholdPercent(pct)
		if c.thresholdPercent != pct {
			t.Errorf("SetThresholdPercent(%f): got %f, want %f", pct, c.thresholdPercent, pct)
		}
	}

	// Invalid values should be ignored — threshold should remain at the last valid value (1.0).
	original := c.thresholdPercent

	for _, pct := range []float64{0.0, -0.5, 1.5, 2.0, -100.0} {
		c.SetThresholdPercent(pct)
		if c.thresholdPercent != original {
			t.Errorf("SetThresholdPercent(%f): threshold changed from %f to %f, expected no change",
				pct, original, c.thresholdPercent)
		}
	}
}

// TestCompressor_TailTokenBudget verifies the TailTokenBudget getter returns
// the value that was supplied to NewCompressor.
func TestCompressor_TailTokenBudget(t *testing.T) {
	t.Parallel()

	c := NewCompressor(3, 50000)
	if got := c.TailTokenBudget(); got != 50000 {
		t.Errorf("TailTokenBudget() = %d, want 50000", got)
	}
}

// TestCompressor_NilAuxProvider verifies that constructing a Compressor without
// calling SetAuxProvider does not panic and leaves the auxProvider field nil.
func TestCompressor_NilAuxProvider(t *testing.T) {
	t.Parallel()

	c := NewCompressor(3, 20000)
	// auxProvider should remain nil — no panic, no forced initialization.
	if c.auxProvider != nil {
		t.Error("auxProvider should be nil when SetAuxProvider is not called")
	}
}

// TestCompressor_InvalidDefaults verifies that passing zero or negative values
// to NewCompressor causes the constructor to fall back to sensible defaults
// (protectFirstN=3, tailTokenBudget=20000).
func TestCompressor_InvalidDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		protectFirstN     int
		tailTokenBudget   int
		wantProtect       int
		wantTailBudget    int
	}{
		{
			name:            "both zero",
			protectFirstN:   0,
			tailTokenBudget: 0,
			wantProtect:     3,
			wantTailBudget:  20000,
		},
		{
			name:            "both negative",
			protectFirstN:   -5,
			tailTokenBudget: -1000,
			wantProtect:     3,
			wantTailBudget:  20000,
		},
		{
			name:            "protectFirstN zero only",
			protectFirstN:   0,
			tailTokenBudget: 5000,
			wantProtect:     3,
			wantTailBudget:  5000,
		},
		{
			name:            "tailTokenBudget negative only",
			protectFirstN:   10,
			tailTokenBudget: -1,
			wantProtect:     10,
			wantTailBudget:  20000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := NewCompressor(tt.protectFirstN, tt.tailTokenBudget)

			if c.protectFirstN != tt.wantProtect {
				t.Errorf("protectFirstN: got %d, want %d", c.protectFirstN, tt.wantProtect)
			}
			if c.tailTokenBudget != tt.wantTailBudget {
				t.Errorf("tailTokenBudget: got %d, want %d", c.tailTokenBudget, tt.wantTailBudget)
			}
		})
	}
}
