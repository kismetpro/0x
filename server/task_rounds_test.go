package server

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kismetpro/0x/agent"
	"github.com/kismetpro/0x/db"
)

func TestTaskRoundWaitsForWholeBatchAndInflightWrites(t *testing.T) {
	e := NewEngine(nil)
	e.rounds.enabled.Store(true)
	e.openTaskRound("1")
	r := e.rounds.rounds["1"]
	r.prepared = true
	r.pending = []*db.Node{{ID: 11}, {ID: 12}, {ID: 13}}
	complete := make(chan *taskRound, 2)
	e.rounds.onComplete = func(_ string, round *taskRound) { complete <- round }
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	e.execCtx["1"], e.execCancel["1"] = ctx, cancel
	for i := 0; i < 3; i++ {
		if n, got := e.takeRoundIntent("1"); n == nil || got != r {
			t.Fatal("batch admission failed")
		}
		if !e.beginTaskOperation("1") {
			t.Fatal("operation rejected")
		}
	}
	if n, _ := e.takeRoundIntent("1"); n != nil {
		t.Fatal("batch expanded")
	}
	for i := 0; i < 3; i++ {
		e.endRoundWork("1", r)
		select {
		case <-complete:
			t.Fatal("released before final database writes")
		default:
		}
		e.decInflight("1")
		if i < 2 && r.finished {
			t.Fatal("released while another worker was active")
		}
	}
	select {
	case got := <-complete:
		if got != r {
			t.Fatal("wrong round")
		}
	case <-time.After(time.Second):
		t.Fatal("no completion signal")
	}
	e.maybeFinishTaskRound("1")
	select {
	case <-complete:
		t.Fatal("duplicate completion")
	default:
	}
	if ctx.Err() != nil || e.IsPaused("1") {
		t.Fatal("automatic yield cancelled or manually paused the task")
	}
}

func TestRoundGenerationRejectsStaleCompletion(t *testing.T) {
	e := NewEngine(nil)
	e.rounds.enabled.Store(true)
	e.openTaskRound("1")
	old := e.rounds.rounds["1"]
	old.finished = true
	e.openTaskRound("1")
	current := e.rounds.rounds["1"]
	current.active, current.planning = 2, 1
	e.endRoundWork("1", old)
	// Do not let a late callback from a disabled/previous round decrement a new one.
	if current.active != 2 || current.planning != 1 {
		t.Fatal("stale round corrupted current counters")
	}
}

func TestEmptyRoundWaitsForHeartbeatOrNewGraphEvent(t *testing.T) {
	e := NewEngine(nil)
	e.rounds.enabled.Store(true)
	task := &Task{ID: "1", notify: make(chan struct{}, 1)}
	e.rounds.idle = map[string]roundIdle{"1": {until: time.Now().Add(time.Hour), revision: task.graphRevision.Load()}}
	if e.roundAdmissionReady(task) {
		t.Fatal("empty task spun a new model call")
	}
	task.Notify()
	if !e.roundAdmissionReady(task) {
		t.Fatal("new graph input did not release idle hold")
	}
	e.rounds.idle["1"] = roundIdle{until: time.Now().Add(-time.Second), revision: task.graphRevision.Load()}
	if !e.roundAdmissionReady(task) {
		t.Fatal("heartbeat did not release idle hold")
	}
}

func TestDetachedMessageCannotEnterRoundHandoff(t *testing.T) {
	e := NewEngine(nil)
	e.rounds.enabled.Store(true)
	e.openTaskRound("1")
	r := e.rounds.rounds["1"]
	r.prepared = true
	task := &Task{ID: "1"}
	if !e.beginDetachedRoundOperation(task) {
		t.Fatal("running round rejected message")
	}
	e.maybeFinishTaskRound("1")
	if r.finished {
		t.Fatal("yielded while message was writing")
	}
	e.decInflight("1")
	if !r.finished {
		t.Fatal("round did not finish after message drained")
	}
	if e.beginDetachedRoundOperation(task) {
		t.Fatal("message bypassed closed round before queued flag committed")
	}
}

func TestQueuedDeadlineDoesNotBypassRoundRobinSlot(t *testing.T) {
	e := NewEngine(nil)
	e.rounds.enabled.Store(true)
	var resolved atomic.Int64
	e.SetAgentResolver(func(*Task) (*agent.Planner, *agent.Worker) { resolved.Add(1); return nil, nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e.runFinalPlannerRound(ctx, &Task{ID: "1", Queued: true}) || resolved.Load() != 0 {
		t.Fatal("queued deadline resolved a model without a task slot")
	}
}

func TestTaskRoundNewInputDuringEmptyPlanIsNotLost(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	defer restoreConcurrencySetting(t, m)()
	if err := m.SetConcurrency(true, 1); err != nil {
		t.Fatal(err)
	}
	task, err := m.CreateTask("empty round wake-up", "local fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer m.DeleteTask(task.ID, DeleteTaskOptions{})
	s := newAdmissionTestServer(m, nil)
	s.ctx = context.Background()
	s.engine.started.Store(task.ID, true)
	s.engine.configureTaskRounds(true)
	r := s.engine.rounds.rounds[task.ID]
	r.planRevision = task.graphRevision.Load()
	r.prepared, r.finished = true, true
	// A hint arrives after the planner consumed its input, but before it parks.
	task.Notify()
	s.finishTaskRound(task.ID, r)
	if task.lifecycleSnapshot().Queued {
		t.Fatal("new input was parked until the old heartbeat")
	}
}

func TestQueuedWorkersNeverResolveLLMProfiles(t *testing.T) {
	m := &Manager{tasks: map[string]*Task{}}
	e := NewEngine(m)
	var resolutions atomic.Int64
	e.SetAgentResolver(func(*Task) (*agent.Planner, *agent.Worker) {
		resolutions.Add(1)
		return nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	task := &Task{ID: "1", Queued: true}
	done := make(chan struct{})
	go func() { defer close(done); e.workerLoop(ctx, task, "worker") }()
	for i := 0; i < 50; i++ {
		task.wakeWorkers()
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if resolutions.Load() != 0 {
		t.Fatalf("waiting task resolved providers %d times", resolutions.Load())
	}
}

func TestRoundRobinThirtyTasksKeepFiveSlotsAndFIFO(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	defer restoreConcurrencySetting(t, m)()
	if err := m.SetConcurrency(true, 5); err != nil {
		t.Fatal(err)
	}
	s := newAdmissionTestServer(m, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.ctx = ctx
	var tasks []*Task
	for i := 0; i < 30; i++ {
		task, err := m.CreateTask(fmt.Sprintf("round-robin-%d", i), "local scheduling test", nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, task)
		defer m.DeleteTask(task.ID, DeleteTaskOptions{})
		if err := m.EnqueueTask(task.ID, "resume"); err != nil {
			t.Fatal(err)
		}
		// Model the long-lived loops without launching actual LLMs.
		s.engine.started.Store(task.ID, true)
	}
	s.engine.configureTaskRounds(true)
	s.reconcileConcurrency()
	rounds := make(map[string]int)
	seen := make(map[string]bool)
	for turn := 0; turn < 90; turn++ {
		var running []*Task
		for _, task := range tasks {
			life := task.lifecycleSnapshot()
			if !life.Queued && !life.Paused {
				running = append(running, task)
				seen[task.ID] = true
			}
		}
		if len(running) != 5 {
			t.Fatalf("turn %d: %d occupied slots", turn, len(running))
		}
		// Finish one batch at a time while all other admitted tasks keep running.
		task := running[0]
		for _, candidate := range running[1:] {
			if rounds[candidate.ID] < rounds[task.ID] {
				task = candidate
			}
		}
		if rounds[task.ID] > 0 && len(seen) < 30 {
			t.Fatal("task received a second round before all tasks were admitted")
		}
		rounds[task.ID]++
		r := s.engine.rounds.rounds[task.ID]
		r.prepared, r.finished = true, true
		r.executed = 3
		s.finishTaskRound(task.ID, r)
		if task.lifecycleSnapshot().Paused || s.engine.IsPaused(task.ID) {
			t.Fatal("round-robin used manual pause")
		}
	}
	if len(seen) != 30 {
		t.Fatalf("only %d tasks got slots", len(seen))
	}
	for _, task := range tasks {
		if rounds[task.ID] < 2 {
			t.Fatalf("task %s starved: %d rounds", task.ID, rounds[task.ID])
		}
	}
}
