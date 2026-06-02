package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryWithBackoff_Success(t *testing.T) {
	callCount := 0
	err := retryWithBackoff(context.Background(), 3, func() error {
		callCount++
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}
}

func TestRetryWithBackoff_SucceedsOnSecondAttempt(t *testing.T) {
	callCount := 0
	err := retryWithBackoff(context.Background(), 3, func() error {
		callCount++
		if callCount < 2 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls, got %d", callCount)
	}
}

func TestRetryWithBackoff_AllFail(t *testing.T) {
	err := retryWithBackoff(context.Background(), 2, func() error {
		return errors.New("always fail")
	})
	if err == nil {
		t.Fatal("expected error when all retries fail")
	}
}

func TestRetryWithBackoff_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := retryWithBackoff(ctx, 3, func() error {
		return errors.New("fail")
	})
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestRetryWithBackoff_ContextCancelledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := retryWithBackoff(ctx, 5, func() error {
		return errors.New("fail")
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRetryWithBackoff_ZeroRetries(t *testing.T) {
	err := retryWithBackoff(context.Background(), 0, func() error {
		return errors.New("fail")
	})
	if err != nil {
		t.Errorf("zero retries should not call fn, got %v", err)
	}
}
