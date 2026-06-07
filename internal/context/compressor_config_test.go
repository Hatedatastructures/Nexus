package context

import (
	"fmt"
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
		name            string
		protectFirstN   int
		tailTokenBudget int
		wantProtect     int
		wantTailBudget  int
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

// ---------------------------------------------------------------------------
// estimateImageTokens
// ---------------------------------------------------------------------------

func TestEstimateImageTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input int
		want  int
	}{
		{0, 0},
		{-1, 0},
		{-5, 0},
		{1, 1600},
		{3, 4800},
		{10, 16000},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("count_%d", tc.input), func(t *testing.T) {
			t.Parallel()
			got := estimateImageTokens(tc.input)
			if got != tc.want {
				t.Errorf("estimateImageTokens(%d) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ShouldCompress
// ---------------------------------------------------------------------------

func TestShouldCompress(t *testing.T) {
	t.Parallel()

	t.Run("below threshold returns false", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		if c.ShouldCompress(100000, 50000) {
			t.Error("should not compress when below threshold")
		}
	})

	t.Run("above threshold returns true", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		if !c.ShouldCompress(100000, 80000) {
			t.Error("should compress when above threshold")
		}
	})

	t.Run("exact threshold returns false", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		// 75% of 100000 = 75000, so 75000 should not compress
		if c.ShouldCompress(100000, 75000) {
			t.Error("at exact threshold should not compress")
		}
	})

	t.Run("cooldown blocks compression", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		// Force anti-thrash by setting consecutiveSummaries to threshold
		c.consecutiveSummaries = 2
		// This call triggers cooldown
		if c.ShouldCompress(100000, 90000) {
			t.Error("cooldown should block compression")
		}
		// Cooldown should now be 3
		if c.antiThrashCooldown != 3 {
			t.Errorf("cooldown should be 3, got %d", c.antiThrashCooldown)
		}
	})

	t.Run("cooldown ticks down", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		c.antiThrashCooldown = 2
		// First tick: cooldown 2->1
		if c.ShouldCompress(100000, 90000) {
			t.Error("cooldown should still block")
		}
		if c.antiThrashCooldown != 1 {
			t.Errorf("cooldown should be 1, got %d", c.antiThrashCooldown)
		}
		// Second tick: cooldown 1->0
		if c.ShouldCompress(100000, 90000) {
			t.Error("cooldown should still block")
		}
		if c.antiThrashCooldown != 0 {
			t.Errorf("cooldown should be 0, got %d", c.antiThrashCooldown)
		}
		// Third call: no cooldown, should compress
		if !c.ShouldCompress(100000, 90000) {
			t.Error("after cooldown expires, should compress again")
		}
	})
}

// ---------------------------------------------------------------------------
// recordCompressionResult
// ---------------------------------------------------------------------------

func TestRecordCompressionResult(t *testing.T) {
	t.Parallel()

	t.Run("effective compression resets counter", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		c.consecutiveSummaries = 1
		// 50% reduction (effective)
		c.recordCompressionResult(1000, 500)
		if c.consecutiveSummaries != 0 {
			t.Errorf("expected counter reset to 0, got %d", c.consecutiveSummaries)
		}
	})

	t.Run("ineffective compression increments counter", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		// 10% reduction (ineffective < 15%)
		c.recordCompressionResult(1000, 900)
		if c.consecutiveSummaries != 1 {
			t.Errorf("expected counter=1, got %d", c.consecutiveSummaries)
		}
	})

	t.Run("zero beforeTokens resets counter", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		c.consecutiveSummaries = 5
		c.recordCompressionResult(0, 0)
		if c.consecutiveSummaries != 0 {
			t.Errorf("zero beforeTokens should reset, got %d", c.consecutiveSummaries)
		}
	})

	t.Run("negative beforeTokens resets counter", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		c.consecutiveSummaries = 3
		c.recordCompressionResult(-10, 0)
		if c.consecutiveSummaries != 0 {
			t.Errorf("negative beforeTokens should reset, got %d", c.consecutiveSummaries)
		}
	})

	t.Run("consecutive ineffective triggers threshold", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		c.recordCompressionResult(1000, 950) // 5% reduction
		c.recordCompressionResult(1000, 950) // 5% reduction
		if c.consecutiveSummaries != 2 {
			t.Errorf("expected 2 consecutive, got %d", c.consecutiveSummaries)
		}
	})

	t.Run("exactly 15 percent reduction resets counter", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		c.consecutiveSummaries = 1
		// 15% exactly: 1000 -> 850
		c.recordCompressionResult(1000, 850)
		if c.consecutiveSummaries != 0 {
			t.Errorf("exactly 15%% should be effective, got counter=%d", c.consecutiveSummaries)
		}
	})
}
