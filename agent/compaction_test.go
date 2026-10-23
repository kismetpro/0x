package agent

import (
	"testing"
	"time"
)

func TestCompactorMaintainThrottle(t *testing.T) {
	c := NewCompactor(nil, "")
	c.maintainEvery = time.Minute
	if !c.claimMaintain(42) {
		t.Fatal("first maintenance was throttled")
	}
	if c.claimMaintain(42) {
		t.Fatal("repeated maintenance ignored the throttle")
	}
	c.mu.Lock()
	c.lastMaintain[42] = time.Now().Add(-2 * time.Minute)
	c.mu.Unlock()
	if !c.claimMaintain(42) {
		t.Fatal("expired maintenance throttle did not release")
	}
}
