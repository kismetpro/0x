package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kismetpro/0x/db"
)

func TestGoalProofReasonRejectsPlaceholders(t *testing.T) {
	for _, reason := range []string{"", "placeholder", "测试目标", "已完成"} {
		if msg := goalProofReasonError(reason); msg == "" {
			t.Fatalf("placeholder reason accepted: %q", reason)
		}
	}
	valid := "finding 123 已在目标资产上复现未授权访问，并取得受保护数据，满足该目标的全部验收条件"
	if msg := goalProofReasonError(valid); msg != "" {
		t.Fatalf("valid reason rejected: %s", msg)
	}
}

func TestProveGoalDefersCompletionWhileIntentsRemain(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTask("goal proof guard", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)

	store := d.Exploration(task.ExplorationID)
	goalID, err := store.AddGoal(map[string]any{"text": "在授权资产上确认并复现未授权访问漏洞"}, "goals")
	if err != nil {
		t.Fatal(err)
	}
	evidenceID, err := store.AddNode(db.KindFact, map[string]any{
		"summary":    "在目标资产上完成未授权访问复现并取得受保护数据",
		"confidence": "observed",
	}, 1, "confirmed", "work#1", nil)
	if err != nil {
		t.Fatal(err)
	}
	intentID, err := store.AddIntent(map[string]any{"summary": "继续核验其余资产与利用面"}, 1, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}

	ts := NewToolSet(store, "planner")
	ts.SetTaskID(task.ID)
	tool := ts.proveGoal()
	call := func(reason string) string {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{
			"goal_id": goalID, "evidence_id": evidenceID, "reason": reason,
		})
		result, err := tool.Call(t.Context(), raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result.Flatten()
	}

	if text := call("placeholder"); !strings.Contains(text, "reason") {
		t.Fatalf("placeholder proof was not rejected: %s", text)
	}
	goal, err := store.GetNode(goalID)
	if err != nil || goal.State != "open" || ts.GoalMet {
		t.Fatalf("rejected proof changed goal/task: goal=%v met=%v err=%v", goal, ts.GoalMet, err)
	}

	valid := "evidence 已复现未授权访问并取得受保护数据，与目标验收条件逐项对应，可以作为当前目标的证明"
	if text := call(valid); !strings.Contains(text, "仍有 1 个 open/running 意图") {
		t.Fatalf("deferred completion not reported: %s", text)
	}
	goal, err = store.GetNode(goalID)
	if err != nil || goal.State != "open" || ts.GoalMet {
		t.Fatalf("active intents allowed marking/completion: goal=%v met=%v err=%v", goal, ts.GoalMet, err)
	}

	if err := store.SetIntentState(intentID, "done"); err != nil {
		t.Fatal(err)
	}
	if text := call(valid); !strings.Contains(text, "任务自动判定完成") {
		t.Fatalf("completion after active work drained not reported: %s", text)
	}
	if !ts.GoalMet {
		t.Fatal("task completion was not set after active intents drained")
	}
}
