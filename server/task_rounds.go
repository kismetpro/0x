package server

import (
	"context"
	"log"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kismetpro/0x/db"
)

func waitWorkerWake(ctx context.Context, wake <-chan struct{}) bool {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-wake:
	case <-timer.C: // safety net for database-only writers, without a hot poll
	}
	return false
}

// A scheduling round admits a finite snapshot of at most Workers intents.
// In-flight agents finish naturally; newly generated work belongs to the next
// round. The planner may still supervise a running batch without expanding it.
type taskRound struct {
	prepared     bool
	preparing    bool
	finished     bool
	planning     int
	active       int
	executed     int
	planRevision uint64
	pending      []*db.Node
}

type taskRoundScheduler struct {
	enabled    atomic.Bool
	mu         sync.Mutex
	rounds     map[string]*taskRound
	idle       map[string]roundIdle
	onComplete func(string, *taskRound)
}

type roundIdle struct {
	until    time.Time
	revision uint64
}

func (s *Server) setTaskScheduling(enabled *bool, limit *int, roundRobin *bool) error {
	s.concMu.Lock()
	defer s.concMu.Unlock()
	on, cap, rotate := false, defaultConcurrencyLimit, false
	rows, err := s.m.pg.Query(`SELECT key,value FROM settings WHERE key IN ($1,$2,$3)`, settingConcurrencyOn, settingConcurrencyLimit, settingTaskRoundRobin)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return err
		}
		switch key {
		case settingConcurrencyOn:
			on = value == "true" || value == "1"
		case settingTaskRoundRobin:
			rotate = value == "true" || value == "1"
		case settingConcurrencyLimit:
			if n, err := strconv.Atoi(value); err == nil && n > 0 {
				cap = n
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if enabled != nil {
		on = *enabled
	}
	if limit != nil {
		cap = *limit
	}
	if cap < 1 {
		cap = defaultConcurrencyLimit
	}
	if roundRobin != nil {
		rotate = *roundRobin
	}
	_, err = s.m.pg.Exec(`INSERT INTO settings(key,value) VALUES ($1,$2),($3,$4),($5,$6)
		ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`,
		settingConcurrencyOn, strconv.FormatBool(on), settingConcurrencyLimit, strconv.Itoa(cap), settingTaskRoundRobin, strconv.FormatBool(rotate))
	if err != nil {
		return err
	}
	// Serialize mode changes with FIFO hand-off; a completion from the old
	// generation must not queue a task that just began a new generation.
	s.engine.configureTaskRounds(on && rotate)
	return nil
}

func (e *Engine) roundAdmissionReady(t *Task) bool {
	if !e.rounds.enabled.Load() {
		return true
	}
	e.rounds.mu.Lock()
	idle, ok := e.rounds.idle[t.ID]
	e.rounds.mu.Unlock()
	return !ok || !time.Now().Before(idle.until) || idle.revision != t.graphRevision.Load()
}

func (e *Engine) configureTaskRounds(enabled bool) {
	if e.rounds.enabled.Swap(enabled) == enabled {
		return
	}
	e.rounds.mu.Lock()
	e.rounds.rounds = make(map[string]*taskRound)
	e.rounds.idle = make(map[string]roundIdle)
	e.rounds.mu.Unlock()
	for _, t := range e.m.List() {
		e.openTaskRound(t.ID)
		t.Notify()
	}
}

func (e *Engine) openTaskRound(id string) {
	e.rounds.mu.Lock()
	defer e.rounds.mu.Unlock()
	if e.rounds.rounds == nil {
		e.rounds.rounds = make(map[string]*taskRound)
	}
	if r := e.rounds.rounds[id]; r == nil || r.finished {
		e.rounds.rounds[id] = &taskRound{}
		delete(e.rounds.idle, id)
	}
}

func (e *Engine) roundRunnable(t *Task) bool {
	life := t.lifecycleSnapshot()
	if life.Queued || life.Paused || e.IsPaused(t.ID) || e.IsDeleting(t.ID) || e.isSettling(t.ID) || isTerminalStatus(life.Status) {
		return false
	}
	if !e.rounds.enabled.Load() {
		return true
	}
	e.rounds.mu.Lock()
	r := e.rounds.rounds[t.ID]
	allowed := r != nil && !r.finished
	e.rounds.mu.Unlock()
	return allowed
}

func (e *Engine) beginRoundPlan(t *Task) (*taskRound, bool) {
	if !e.rounds.enabled.Load() {
		return nil, true
	}
	e.rounds.mu.Lock()
	defer e.rounds.mu.Unlock()
	r := e.rounds.rounds[t.ID]
	if r == nil || r.finished || r.preparing {
		return nil, false
	}
	r.planning++
	r.planRevision = t.graphRevision.Load()
	return r, true
}

func (e *Engine) roundWorkerRunnable(t *Task) bool {
	if !e.roundRunnable(t) {
		return false
	}
	if !e.rounds.enabled.Load() {
		return true
	}
	e.rounds.mu.Lock()
	r := e.rounds.rounds[t.ID]
	ready := r != nil && r.prepared && !r.finished && len(r.pending) > 0
	e.rounds.mu.Unlock()
	return ready
}

func (e *Engine) beginDetachedRoundOperation(t *Task) bool {
	if !e.rounds.enabled.Load() {
		return e.beginTaskOperation(t.ID)
	}
	e.rounds.mu.Lock()
	defer e.rounds.mu.Unlock()
	r := e.rounds.rounds[t.ID]
	if r == nil || r.finished || t.lifecycleSnapshot().Queued {
		return false
	}
	// Reserve the writer while holding the same lock as round completion. A
	// worker message cannot sneak into the quiescent->queued hand-off window.
	return e.beginTaskOperation(t.ID)
}

func (e *Engine) endRoundPlan(t *Task, round *taskRound) {
	t.wakeWorkers()
	if round == nil {
		return
	}
	e.prepareTaskRound(t, round)
	terminal := isTerminalStatus(t.lifecycleSnapshot().Status)
	e.rounds.mu.Lock()
	if r := e.rounds.rounds[t.ID]; r == round && r.planning > 0 {
		r.planning--
		if terminal {
			r.pending = nil
		}
	}
	e.rounds.mu.Unlock()
	e.maybeFinishTaskRound(t.ID)
}

func (e *Engine) prepareTaskRound(t *Task, expected *taskRound) {
	if !e.rounds.enabled.Load() {
		return
	}
	e.rounds.mu.Lock()
	r := e.rounds.rounds[t.ID]
	if r == nil || (expected != nil && r != expected) || r.prepared || r.preparing || r.finished {
		e.rounds.mu.Unlock()
		return
	}
	r.preparing = true
	e.rounds.mu.Unlock()
	pending, err := t.Store.Frontier(e.m.Workers())
	e.rounds.mu.Lock()
	if e.rounds.rounds[t.ID] == r {
		r.preparing = false
		if err == nil {
			r.pending, r.prepared = pending, true
		}
	}
	e.rounds.mu.Unlock()
	if err != nil {
		log.Printf("[round-robin] task %s snapshot: %v", t.ID, err)
		return
	}
	t.wakeWorkers()
}

func (e *Engine) takeRoundIntent(id string) (*db.Node, *taskRound) {
	e.rounds.mu.Lock()
	defer e.rounds.mu.Unlock()
	r := e.rounds.rounds[id]
	if r == nil || !r.prepared || r.finished || len(r.pending) == 0 {
		return nil, nil
	}
	n := r.pending[0]
	r.pending[0] = nil
	r.pending = r.pending[1:]
	r.active++
	r.executed++
	return n, r
}

func (e *Engine) endRoundWork(id string, round *taskRound) {
	e.rounds.mu.Lock()
	if r := e.rounds.rounds[id]; r == round && r.active > 0 {
		r.active--
	}
	e.rounds.mu.Unlock()
	e.maybeFinishTaskRound(id)
}

func (e *Engine) maybeFinishTaskRound(id string) {
	if !e.rounds.enabled.Load() || e.inflightCount(id) != 0 {
		return
	}
	e.rounds.mu.Lock()
	r := e.rounds.rounds[id]
	complete := r != nil && r.prepared && !r.finished && !r.preparing && r.planning == 0 && r.active == 0 && len(r.pending) == 0 && e.inflightCount(id) == 0
	if complete {
		r.finished = true
	}
	e.rounds.mu.Unlock()
	// Completion can run under admission/deletion locks. One callback per round
	// moves the durable queue under concMu, never in an agent's critical section.
	if complete && e.rounds.onComplete != nil {
		go e.rounds.onComplete(id, r)
	}
}

func (s *Server) finishTaskRound(id string, completed *taskRound) {
	s.concMu.Lock()
	defer s.concMu.Unlock()
	s.engine.rounds.mu.Lock()
	current := s.engine.rounds.rounds[id]
	valid := current == completed && completed.finished
	s.engine.rounds.mu.Unlock()
	if !valid {
		return
	}
	t, ok := s.m.Task(id)
	if !ok || s.ctx.Err() != nil || s.engine.IsDeleting(id) {
		return
	}
	life := t.lifecycleSnapshot()
	if life.Paused || life.Queued {
		return
	}
	if isTerminalStatus(life.Status) {
		s.reconcileConcurrencyLocked()
		return
	}
	if !s.engine.rounds.enabled.Load() {
		return
	}
	hasWork, err := t.Store.HasActiveIntent()
	if err != nil {
		s.engine.openTaskRound(id)
		t.Notify()
		return
	}
	if !hasWork && completed.executed == 0 {
		s.engine.rounds.mu.Lock()
		if s.engine.rounds.idle == nil {
			s.engine.rounds.idle = make(map[string]roundIdle)
		}
		s.engine.rounds.idle[id] = roundIdle{until: time.Now().Add(plannerHeartbeatInterval(t)), revision: completed.planRevision}
		s.engine.rounds.mu.Unlock()
	} else if !s.hasReadyQueuedTask(id) {
		// With no contenders and no frontier, retain the normal event/heartbeat
		// cadence instead of spinning empty planning calls to fill a slot.
		if !hasWork {
			s.engine.openTaskRound(id)
			if completed.executed > 0 {
				t.Notify() // judge the completed batch once, not an empty-plan spin
			}
			return
		}
	}
	if err := s.m.ApplyTaskAdmission(id, life.Status, life.Status, true, "resume", false); err != nil {
		log.Printf("[round-robin] task %s enqueue: %v", id, err)
		s.engine.openTaskRound(id)
		t.Notify()
		return
	}
	s.engine.emitActivity(t, db.Activity{Worker: "system", Kind: "text", Summary: "本轮已完成，进入轮询等待；按 FIFO 顺序自动继续（非手动暂停）"})
	s.reconcileConcurrencyLocked()
}
