package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kismetpro/0x/db"
)

func TestFindingNumberMatchesTaskGlobalAndExportAPI(t *testing.T) {
	s, recorded, req := trafficEvidenceServer(t)
	if recorded.Number != db.FindingNumber(recorded.FindingID) {
		t.Fatal("recording tool did not return number")
	}
	w := req("GET", fmt.Sprintf("/api/exploration/findings/%d", recorded.FindingID), "")
	var detail FindingDTO
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || detail.Number != recorded.Number {
		t.Fatalf("detail: %d %s", w.Code, w.Body)
	}
	f, err := s.m.pg.GetFinding(recorded.FindingID)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := s.m.pg.FindingMetaByNodeID(*f.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.m.pg.Exploration(s.m.ResolveTask(fmt.Sprint(*f.TaskID)).ExpID).GetNode(recorded.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	rows := findingDTOsForOwner(fmt.Sprint(*f.TaskID), "fixture", []*db.Node{node}, meta, nil)
	if len(rows) != 1 || rows[0].Number != detail.Number {
		t.Fatal("task/global number diverged")
	}
	w = req("GET", fmt.Sprintf("/api/report?task=%d", *f.TaskID), "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), recorded.Number) {
		t.Fatalf("task report: %d %s", w.Code, w.Body)
	}
	w = req("GET", fmt.Sprintf("/api/exploration/findings/export?scope=selected&ids=%d&format=json", recorded.FindingID), "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), recorded.Number) {
		t.Fatalf("JSON export: %d %s", w.Code, w.Body)
	}
}

func TestTaskRoundRobinSettingPersistsAndRequiresCap(t *testing.T) {
	s, _, req := trafficEvidenceServer(t)
	old := s.m.pg.GetBool(settingTaskRoundRobin, false)
	defer s.m.pg.SetBool(settingTaskRoundRobin, old)
	defer restoreConcurrencySetting(t, s.m)()
	for _, tc := range []struct {
		body   string
		active bool
	}{
		{`{"task_concurrency_enabled":true,"task_concurrency_limit":5,"task_round_robin":true}`, true},
		{`{"task_concurrency_enabled":false}`, false},
		{`{"task_concurrency_enabled":true}`, true},
		{`{"task_round_robin":false}`, false},
	} {
		w := req("PUT", "/api/settings", tc.body)
		if w.Code != 200 {
			t.Fatalf("settings: %d %s", w.Code, w.Body)
		}
		if s.m.TaskRoundRobin() != tc.active || s.engine.rounds.enabled.Load() != tc.active {
			t.Fatal("persisted/runtime scheduling mismatch")
		}
	}
}
