package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/kismetpro/0x/agent"
	"github.com/kismetpro/0x/db"
	"github.com/kismetpro/0x/guard"
	"github.com/kismetpro/0x/report"
)

// Deterministic model fixture: a real HTTP probe, real report_finding tool and
// normal agent/Engine/PG execution. No third-party target or model is contacted.
type localRoundProvider struct {
	mu     sync.Mutex
	active map[int64]int
	peak   int
}

func (p *localRoundProvider) Stream(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{}, fmt.Errorf("fixture requires non-streaming"))
	}
}

func (p *localRoundProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	ri := agent.RunInfoFrom(ctx)
	p.mu.Lock()
	p.active[ri.TaskID]++
	if len(p.active) > p.peak {
		p.peak = len(p.active)
	}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.active[ri.TaskID]--
		if p.active[ri.TaskID] == 0 {
			delete(p.active, ri.TaskID)
		}
		p.mu.Unlock()
	}()
	if sleepCtx(ctx, 10*time.Millisecond) {
		return llm.Message{}, "", llm.Usage{}, ctx.Err()
	}
	msg := llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("Local fixture verified; keep the goal open for subsequent batches.")}}
	if ri.IntentID == 0 {
		return msg, "end_turn", llm.Usage{}, nil
	}
	var probed, reported bool
	for _, m := range req.Messages {
		for _, b := range m.Content {
			if b.Type == llm.BlockToolUse {
				probed = probed || b.Name == "local_probe"
				reported = reported || b.Name == "report_finding"
			}
		}
	}
	if reported {
		return msg, "end_turn", llm.Usage{}, nil
	}
	name, input := "local_probe", map[string]any{}
	if probed {
		name = "report_finding"
		input = map[string]any{"vulnclass": "LOCAL_FIXTURE_IDOR", "severity": "low", "name": fmt.Sprintf("Isolated record %d", ri.IntentID), "summary": "Local fixture returned an unprotected test record", "evidence": "HTTP 200 fixture-owned-record; local_probe used an in-process loopback target only", "intent_id": ri.IntentID}
	}
	raw, _ := json.Marshal(input)
	msg.Content = []llm.ContentBlock{{Type: llm.BlockToolUse, ID: name + strconv.FormatInt(ri.IntentID, 10), Name: name, Input: raw}}
	return msg, "tool_use", llm.Usage{}, nil
}

func TestRoundRobinRealEngineLocalTargetThirtyTasks(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	defer restoreConcurrencySetting(t, m)()
	if err := m.SetConcurrency(true, 5); err != nil {
		t.Fatal(err)
	}
	oldWorkers := m.Workers()
	defer m.SetWorkers(oldWorkers)
	if err := m.SetWorkers(3); err != nil {
		t.Fatal(err)
	}
	var probes atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		io.WriteString(w, "fixture-owned-record")
	}))
	defer target.Close()
	probe := actool.Build(actool.Spec{Name: "local_probe", Description: "Read only the in-process local fixture", Schema: map[string]any{"type": "object"}, Run: func(ctx context.Context, _ json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target.URL+"/records/2", nil)
		res, err := target.Client().Do(req)
		if err != nil {
			return actool.Result{}, err
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		return actool.Text(string(body)), err
	}})
	provider := &localRoundProvider{active: make(map[int64]int)}
	planner := agent.NewPlanner(provider, "local", m.dir, nil, 0, 8)
	planner.SetNonStreaming(func() bool { return true })
	worker := agent.NewWorker(provider, "local", m.dir, nil, 0, 8, probe)
	worker.SetNonStreaming(func() bool { return true })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s := &Server{m: m, engine: NewEngine(m), ctx: ctx}
	s.engine.debounce = 5 * time.Millisecond
	s.engine.SetAuthoritativeAgentResolver(func(*Task) (*agent.Planner, *agent.Worker) { return planner, worker })
	s.engine.rounds.onComplete = s.finishTaskRound
	s.engine.configureTaskRounds(true)
	var tasks []*Task
	defer func() {
		cancel()
		for _, task := range tasks {
			s.engine.StopTask(task.ID)
			// Production intentionally retains notification history after deletion;
			// fixtures must remove only their own events to keep fan-out tests isolated.
			m.pg.Exec(`DELETE FROM notification_events WHERE finding_id IN (SELECT id FROM findings WHERE task_id=$1)`, task.ID)
			m.DeleteTask(task.ID, DeleteTaskOptions{DeleteFindings: true})
		}
	}()
	for i := 0; i < 30; i++ {
		task, err := m.CreateTask(fmt.Sprintf("local round fixture %d", i), "Verify each of six independent local records", nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, task)
		task.Guard = guard.New()
		if _, err := task.Store.AddGoal(map[string]any{"text": "complete local fixture"}, "test"); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 6; j++ {
			if _, err := task.Store.AddIntent(map[string]any{"summary": "Probe and report the isolated local fixture"}, 5, nil, "test"); err != nil {
				t.Fatal(err)
			}
		}
		if err := m.EnqueueTask(task.ID, "resume"); err != nil {
			t.Fatal(err)
		}
	}
	s.reconcileConcurrency()
	for {
		var done int
		for _, task := range tasks {
			nodes, err := task.Store.ListByKind(db.KindIntent, 20)
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range nodes {
				if n.State == "done" {
					done++
				}
			}
		}
		if done == 180 {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("only %d/180 intents completed; probes=%d", done, probes.Load())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if probes.Load() != 180 {
		t.Fatalf("lost/replayed probes: %d", probes.Load())
	}
	provider.mu.Lock()
	peak := provider.peak
	provider.mu.Unlock()
	if peak > 5 {
		t.Fatalf("task concurrency breached: %d", peak)
	}
	seen := map[string]bool{}
	for _, task := range tasks {
		fs, err := m.pg.ListFindingsForExport(db.FindingFilter{TaskID: task.ID}, nil)
		if err != nil || len(fs) != 6 {
			t.Fatalf("task %s: reports=%d err=%v", task.ID, len(fs), err)
		}
		for _, f := range fs {
			if seen[f.Number()] {
				t.Fatal("duplicate number")
			}
			seen[f.Number()] = true
			if !strings.Contains(report.SingleFindingMarkdown(f, time.Now()), f.Number()) {
				t.Fatal("export lost number")
			}
		}
	}
	t.Logf("30 tasks x 6 intents: %d real local HTTP probes, %d uniquely numbered reports; peak simultaneously calling tasks=%d", probes.Load(), len(seen), peak)
}
