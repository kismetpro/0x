package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/kismetpro/0x/agent"
	"github.com/kismetpro/0x/db"
	"github.com/kismetpro/0x/report"
)

// Explicit opt-in: spends a small amount of live API credit. The model can only
// read two loopback fixture records and submit a finding; no shell or arbitrary
// URL tool is exposed. Normal tests never call an external model.
func TestLiveLLMLoopbackFindingAndExport(t *testing.T) {
	if os.Getenv("ZEROX_LIVE_LLM_TEST") != "1" {
		t.Skip("live API test is opt-in")
	}
	cfg, ok := agent.FromEnv()
	if !ok {
		t.Fatal("configure the local LLM environment first")
	}
	provider, err := cfg.NewProvider()
	if err != nil {
		t.Fatal("invalid provider configuration")
	}
	dsn, _, err := db.DSN()
	if err != nil {
		t.Fatal(err)
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	task, err := pg.CreateTask("owned loopback live-model fixture", "verify object authorization on two local records", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		pg.Exec(`DELETE FROM notification_events WHERE finding_id IN (SELECT id FROM findings WHERE task_id=$1)`, task.ID)
		pg.DeleteTaskCascade(task.ID, false, true)
	}()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Fixture-User") != "user1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id := r.URL.Query().Get("id")
		if id != "1" && id != "2" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":%s,"owner":"user%s","canary":"fixture-record-%s"}`, id, id, id)
	}))
	defer target.Close()
	props := func(p map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": p, "required": required, "additionalProperties": false}
	}
	request := llm.CompletionRequest{
		System:    []string{"This is an authorized, isolated test fixture, not an external target. The authenticated test caller is user1. Check only the two provided local records for broken object authorization, compare the baseline and the second record, and report only if verified. Include the exact fixture-record-1 and fixture-record-2 markers and observed owners in evidence. Use only the provided tools. End after reporting."},
		Messages:  []llm.Message{llm.UserText("Assess whether user1 can read user2's local fixture record, then submit one verified finding.")},
		MaxTokens: 1200,
		Tools: []llm.ToolSchema{
			{Name: "fixture_record", Description: "Read one record from the owned loopback fixture as user1; only IDs 1 and 2 exist", InputSchema: props(map[string]any{"id": map[string]any{"type": "integer", "enum": []int{1, 2}}}, "id")},
			{Name: "report_finding", Description: "Persist the verified local finding with exact observed evidence", InputSchema: props(map[string]any{"name": map[string]any{"type": "string"}, "evidence": map[string]any{"type": "string"}}, "name", "evidence")},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	seen := map[int]bool{}
	for turn := 0; turn < 6; turn++ {
		verifiedBeforeCall := seen[1] && seen[2]
		msg, _, _, err := provider.Complete(ctx, request)
		if err != nil {
			t.Fatal(strings.ReplaceAll(err.Error(), cfg.APIKey, "[REDACTED]"))
		}
		request.Messages = append(request.Messages, msg)
		for _, call := range msg.Content {
			if call.Type != llm.BlockToolUse {
				continue
			}
			result := "Unsupported tool"
			switch call.Name {
			case "fixture_record":
				var in struct {
					ID int `json:"id"`
				}
				if json.Unmarshal(call.Input, &in) != nil || (in.ID != 1 && in.ID != 2) {
					t.Fatal("invalid fixture ID")
				}
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target.URL+"?id="+strconv.Itoa(in.ID), nil)
				req.Header.Set("X-Fixture-User", "user1")
				res, err := target.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(res.Body)
				res.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				seen[in.ID] = true
				result = string(body)
			case "report_finding":
				var in struct {
					Name     string `json:"name"`
					Evidence string `json:"evidence"`
				}
				if json.Unmarshal(call.Input, &in) != nil || !verifiedBeforeCall || !strings.Contains(in.Evidence, "fixture-record-1") || !strings.Contains(in.Evidence, "fixture-record-2") {
					t.Fatal("model attempted an unverified report")
				}
				recorded, err := pg.Exploration(task.ExplorationID).RecordFinding(ctx, db.RecordFindingInput{TaskID: task.ID, ExplorationID: task.ExplorationID, VulnClass: "LOCAL_FIXTURE_IDOR", Name: in.Name, Severity: "low", Summary: "Verified local fixture authorization defect", Evidence: in.Evidence, Worker: "live-local-test"})
				if err != nil {
					t.Fatal(err)
				}
				finding, err := pg.GetFinding(recorded.FindingID)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(report.SingleFindingMarkdown(finding, time.Now()), recorded.Number) {
					t.Fatal("export omitted number")
				}
				t.Logf("Live %s: both loopback records verified; finding %s persisted and exported with its number", cfg.Model, recorded.Number)
				return
			}
			request.Messages = append(request.Messages, llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResultText(call.ID, result, false)}})
		}
	}
	t.Fatal("model did not verify and report within the bounded live test")
}
