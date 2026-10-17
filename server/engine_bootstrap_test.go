package server

import (
	"context"
	"testing"
	"time"

	"github.com/kismetpro/0x/agent"
)

// The exact regression behind the "63 tasks completed without doing anything"
// incident: before the first goal decomposition lands, a task has no goals at all.
// That must never be read as "every goal settled".
func TestDecideGoallessRequiresBootstrapForGoallessTasks(t *testing.T) {
	cases := []struct {
		name          string
		bootstrapDone bool
		total, open   int
		activeIntent  bool
		want          goallessOutcome
	}{
		{"拆解未完成且零目标", false, 0, 0, false, goallessWait},
		{"拆解未完成且零目标但在跑意图", false, 0, 0, true, goallessWait},
		{"拆解完成但确实没有目标", true, 0, 0, false, goallessComplete},
		{"拆解完成但仍有在跑意图", true, 0, 0, true, goallessIdle},
		{"目标已在库(进程重启后)且全部了结", false, 2, 0, false, goallessComplete},
		{"目标已在库且存在在跑意图", false, 2, 0, true, goallessIdle},
		{"仍有未了结目标", true, 2, 1, false, goallessPlan},
		{"仍有未了结目标且拆解未完成", false, 1, 1, true, goallessPlan},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideGoalless(tc.bootstrapDone, tc.total, tc.open, tc.activeIntent); got != tc.want {
				t.Fatalf("decideGoalless(%v,%d,%d,%v)=%d want %d",
					tc.bootstrapDone, tc.total, tc.open, tc.activeIntent, got, tc.want)
			}
		})
	}
}

func TestBootstrapAllowsSingleDecomposition(t *testing.T) {
	e := NewEngine(nil)
	if !e.beginBootstrap("1") {
		t.Fatal("first decomposition must be admitted")
	}
	if e.beginBootstrap("1") {
		t.Fatal("concurrent duplicate decomposition was admitted")
	}
	if e.bootstrapCompleted("1") {
		t.Fatal("in-flight decomposition reported as complete")
	}
	e.markBootstrapDone("1")
	if !e.bootstrapCompleted("1") {
		t.Fatal("completed decomposition not recorded")
	}
	if !e.beginBootstrap("2") {
		t.Fatal("a different task must not be blocked")
	}
	e.forgetBootstrap("1")
	if e.bootstrapCompleted("1") {
		t.Fatal("forgetBootstrap left stale state")
	}
}

func TestGoalCountsDistinguishesMissingFromSettled(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer m.Close()
	task, err := m.CreateTask("goal counts", "local fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer m.DeleteTask(task.ID, DeleteTaskOptions{})
	store := m.pg.Exploration(task.ExpID)
	if total, open, err := store.GoalCounts(); err != nil || total != 0 || open != 0 {
		t.Fatalf("fresh task: total=%d open=%d err=%v", total, open, err)
	}
	if taskHasBootstrapEvidence(task) {
		t.Fatal("a task with no goals/intents/first-run must not look bootstrapped")
	}
	if _, err := store.AddGoal(map[string]any{"text": "fixture goal"}, "test"); err != nil {
		t.Fatal(err)
	}
	if total, open, err := store.GoalCounts(); err != nil || total != 1 || open != 1 {
		t.Fatalf("after AddGoal: total=%d open=%d err=%v", total, open, err)
	}
	if !taskHasBootstrapEvidence(task) {
		t.Fatal("a task holding a goal must look bootstrapped")
	}
}

// End-to-end regression: loops running while the first goal decomposition is
// still in flight must not finalize the task; once the decomposition is known to
// be finished, a genuinely goalless task keeps the legacy "done" semantics.
func TestPlannerDoesNotCompleteTaskBeforeGoalDecomposition(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer m.Close()
	task, err := m.CreateTask("bootstrap race", "wait for decomposition", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := NewEngine(m)
	e.debounce = 5 * time.Millisecond
	planner, worker := new(agent.Planner), new(agent.Worker)
	e.SetAuthoritativeAgentResolver(func(*Task) (*agent.Planner, *agent.Worker) { return planner, worker })
	defer func() {
		e.StopTask(task.ID)
		m.DeleteTask(task.ID, DeleteTaskOptions{})
	}()
	e.Run(ctx, task)
	time.Sleep(400 * time.Millisecond)
	if got := m.TaskStatus(task.ID); isTerminalStatus(got) {
		t.Fatalf("task was finalized as %q while its goal decomposition was still in flight", got)
	}
	if e.bootstrapCompleted(task.ID) {
		t.Fatal("engine claimed the decomposition finished on its own")
	}
	// 拆解失败/目标为空：拆解确实结束后仍保持既有语义（收尾为 done）。
	e.markBootstrapDone(task.ID)
	task.Notify()
	deadline := time.Now().Add(5 * time.Second)
	for m.TaskStatus(task.ID) != "done" {
		if time.Now().After(deadline) {
			t.Fatalf("task stayed %q after its decomposition finished with no goals", m.TaskStatus(task.ID))
		}
		time.Sleep(20 * time.Millisecond)
	}
}
