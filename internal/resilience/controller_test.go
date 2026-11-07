// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package resilience

import (
	"testing"
	"time"
)

func TestControllerAdmitRefusesOnOpenBreaker(t *testing.T) {
	ResetAllBreakers()
	c := NewController(BreakerOptions{FailureThreshold: 1, ResetTimeout: time.Minute}, SemaphoreOptions{})

	if d := c.Admit("openai", "acct", "direct"); !d.Allowed {
		t.Fatalf("Admit() refused a CLOSED breaker: %+v", d)
	}
	c.ReportFailure("openai", "acct", "direct", nil, 503)

	d := c.Admit("openai", "acct", "direct")
	if d.Allowed {
		t.Fatal("Admit() allowed a request while the breaker is OPEN")
	}
	if d.Reason != "breaker" {
		t.Errorf("Admit() reason = %q, want %q", d.Reason, "breaker")
	}
	if d.RetryAfter <= 0 {
		t.Errorf("Admit() retryAfter = %v, want a positive duration", d.RetryAfter)
	}
}

func TestControllerReportFailureIgnoresAccountStatuses(t *testing.T) {
	ResetAllBreakers()
	c := NewController(BreakerOptions{FailureThreshold: 1, ResetTimeout: time.Minute}, SemaphoreOptions{})

	// 401/429/400 say something about the account or the request, not the
	// provider: reporting them must leave the breaker CLOSED.
	for _, status := range []int{401, 429, 400, 403} {
		c.ReportFailure("openai", "acct", "direct", nil, status)
	}
	if st := c.BreakerStatus("openai"); st.State != StateClosed {
		t.Errorf("breaker state after account-level failures = %s, want CLOSED", st.State)
	}

	c.ReportFailure("openai", "acct", "direct", nil, 500)
	if st := c.BreakerStatus("openai"); st.State != StateOpen {
		t.Errorf("breaker state after a 500 = %s, want OPEN", st.State)
	}
}

func TestControllerModelLockoutPassthrough(t *testing.T) {
	// The breaker registry is process-wide; other tests leave "openai" OPEN.
	ResetAllBreakers()
	base := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	clock := base
	original := modelLocks.now
	modelLocks.now = func() time.Time { return clock }
	t.Cleanup(func() { modelLocks.now = original })
	ResetModelLockouts()
	t.Cleanup(ResetModelLockouts)

	c := DefaultController()

	// A 429 on one model locks only that tuple, through the Controller facade.
	c.RecordModelFailure("openai", "conn-1", "gpt-4o", 429, 5*time.Minute)
	if locked, until := c.ModelLocked("openai", "conn-1", "gpt-4o"); !locked {
		t.Fatal("ModelLocked() = false after a 429, want true")
	} else if want := base.Add(5 * time.Minute); !until.Equal(want) {
		t.Errorf("ModelLocked() until = %v, want %v", until, want)
	}
	if locked, _ := c.ModelLocked("openai", "conn-1", "gpt-4o-mini"); locked {
		t.Error("a sibling model on the same connection is locked too")
	}

	// The model lockout must not charge the provider breaker.
	if st := c.BreakerStatus("openai"); st.State != StateClosed {
		t.Errorf("breaker state after a model 429 = %s, want CLOSED", st.State)
	}

	// A success clears it.
	c.RecordModelSuccess("openai", "conn-1", "gpt-4o")
	if locked, _ := c.ModelLocked("openai", "conn-1", "gpt-4o"); locked {
		t.Error("ModelLocked() = true after a success, want false")
	}
}
