package server

import (
	"log"
	"sync"

	"github.com/kismetpro/0x/db"
)

// 首轮目标拆解（bootstrap）与 goalless 收尾判定。
//
// 任务的 goal 节点是首次准入时由 createGoals 写库的。在它落库之前，任务处于
// 「一个目标都没有」的状态，而 planner 读到的 HasOpenGoal() 同样是 false——
// 这与「所有目标都已了结」无法区分。暂停 → 重启 → 继续时，循环会被提前拉起，
// Resume 的 Notify 立刻触发一轮规划，于是空窗期被误判成「目标已全部达成」，
// 任务被错误收尾为 done（实测 63 个任务在同一分钟被误标）。
//
// 因此这里做两件事：
//  1. bootstrap 完成之前，goalless 分支一律不动作（既不规划也不收尾）；
//  2. 同一任务同一时刻只允许一次 bootstrap 拆解，避免重复并发拆解写出重复目标。
type bootstrapTracker struct {
	mu     sync.Mutex
	states map[string]*bootstrapState
}

type bootstrapState struct {
	done       bool
	inProgress bool
	waitLogged bool
}

func (b *bootstrapTracker) stateLocked(id string) *bootstrapState {
	if b.states == nil {
		b.states = make(map[string]*bootstrapState)
	}
	st := b.states[id]
	if st == nil {
		st = &bootstrapState{}
		b.states[id] = st
	}
	return st
}

// beginBootstrap reserves the one-shot goal decomposition for a task. Returns
// false when another decomposition for the same task is still in flight.
func (e *Engine) beginBootstrap(taskID string) bool {
	e.bootstrap.mu.Lock()
	defer e.bootstrap.mu.Unlock()
	st := e.bootstrap.stateLocked(taskID)
	if st.inProgress {
		return false
	}
	st.inProgress = true
	return true
}

// endBootstrap releases the reservation without claiming the decomposition
// finished (the caller may have been cancelled mid-flight).
func (e *Engine) endBootstrap(taskID string) {
	e.bootstrap.mu.Lock()
	defer e.bootstrap.mu.Unlock()
	e.bootstrap.stateLocked(taskID).inProgress = false
}

// markBootstrapDone records that the first goal decomposition for this task has
// finished (成功、失败回退或目标为空都算)，此后 goalless 分支才允许收尾。
func (e *Engine) markBootstrapDone(taskID string) {
	e.bootstrap.mu.Lock()
	defer e.bootstrap.mu.Unlock()
	st := e.bootstrap.stateLocked(taskID)
	st.done, st.inProgress = true, false
}

// bootstrapCompleted reports whether the first goal decomposition finished.
func (e *Engine) bootstrapCompleted(taskID string) bool {
	e.bootstrap.mu.Lock()
	defer e.bootstrap.mu.Unlock()
	return e.bootstrap.stateLocked(taskID).done
}

// forgetBootstrap drops all bootstrap bookkeeping for a removed task.
func (e *Engine) forgetBootstrap(taskID string) {
	e.bootstrap.mu.Lock()
	delete(e.bootstrap.states, taskID)
	e.bootstrap.mu.Unlock()
}

// noteBootstrapWait logs the wait state once per task so a stalled first
// decomposition stays visible without spamming every heartbeat.
func (e *Engine) noteBootstrapWait(t *Task) {
	e.bootstrap.mu.Lock()
	defer e.bootstrap.mu.Unlock()
	st := e.bootstrap.stateLocked(t.ID)
	if st.waitLogged {
		return
	}
	st.waitLogged = true
	log.Printf("[goalless] task %s 尚无目标且首轮目标拆解未完成，保持运行等待拆解结果（不再误判为已完成）", t.ID)
}

// goallessOutcome is the planner's decision for a task with no open goals.
type goallessOutcome int

const (
	goallessPlan     goallessOutcome = iota // 还有 open 目标 → 正常规划
	goallessWait                            // 首轮目标拆解尚未落库 → 既不规划也不收尾
	goallessIdle                            // 目标已了结、但在跑意图未收干 → 静默等待
	goallessComplete                        // 目标已了结且意图收干 → 收尾落 done
)

// decideGoalless is the pure policy behind the goalless branch. bootstrapDone is
// what separates "no goals yet" (still decomposing) from "all goals settled".
func decideGoalless(bootstrapDone bool, totalGoals, openGoals int, activeIntent bool) goallessOutcome {
	if openGoals > 0 {
		return goallessPlan
	}
	// 一个目标都不存在、且拆解尚未完成：这是首次准入的空窗期，既没有目标可规划，
	// 也绝不能当成「全部达成」。已有目标则说明拆解确实发生过（即使进程中途重启）。
	if totalGoals == 0 && !bootstrapDone {
		return goallessWait
	}
	if activeIntent {
		return goallessIdle
	}
	return goallessComplete
}

// taskHasBootstrapEvidence reports whether a task clearly passed its first goal
// decomposition: a goal/intent node exists, or it already ran a real round.
func taskHasBootstrapEvidence(t *Task) bool {
	if t.lifecycleSnapshot().FirstRunAt > 0 {
		return true
	}
	stats, err := t.Store.Stats()
	if err != nil {
		// 读不到状态时按「已拆解」处理，宁可多跑一轮也不让任务被永久搁置。
		log.Printf("[engine] task %s 读取节点统计失败，按已拆解处理: %v", t.ID, err)
		return true
	}
	return stats[string(db.KindGoal)] > 0 || stats[string(db.KindIntent)] > 0
}
