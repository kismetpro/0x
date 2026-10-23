package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFindingDuplicateConfirmationSettingAndToolGates(t *testing.T) {
	s, task, ids := testCaseServer(t)
	old, hadOld, err := s.m.pg.GetSetting(settingFindingDuplicateConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if hadOld {
			_ = s.m.pg.SetSetting(settingFindingDuplicateConfirmation, old)
			return
		}
		_, _ = s.m.pg.Exec(`DELETE FROM settings WHERE key=$1`, settingFindingDuplicateConfirmation)
	}()
	if _, err := s.m.pg.Exec(`DELETE FROM settings WHERE key=$1`, settingFindingDuplicateConfirmation); err != nil {
		t.Fatal(err)
	}
	if !s.findingDuplicateConfirmationEnabled() || s.settingsPayload()[settingFindingDuplicateConfirmation] != true {
		t.Fatal("duplicate confirmation must default to enabled")
	}

	a := findingCaseRequest{
		TaskID: json.RawMessage(fmt.Sprint(task)),
		Title:  "订单对象越权",
		Reason: "同一 handler 未校验订单所有者",
		IDs:    []json.RawMessage{json.RawMessage(fmt.Sprint(ids[0])), json.RawMessage(fmt.Sprint(ids[1]))},
	}
	if _, err := s.performFindingCaseTool(context.Background(), "merge_finding_records", a); err == nil || !strings.Contains(err.Error(), "suggest_finding_merge") {
		t.Fatalf("enabled auto-merge not blocked: %v", err)
	}
	if _, err := s.performFindingCaseTool(context.Background(), "search_finding_duplicates", findingCaseRequest{FindingID: a.IDs[0]}); err != nil {
		t.Fatalf("enabled duplicate search failed: %v", err)
	}
	if _, err := s.performFindingCaseTool(context.Background(), "suggest_finding_merge", a); err != nil {
		t.Fatalf("enabled suggestion failed: %v", err)
	}

	w := httptest.NewRecorder()
	s.putSettings(w, httptest.NewRequest("PUT", "/api/settings", strings.NewReader(`{"finding_duplicate_confirmation_enabled":false}`)))
	if w.Code != 200 || s.findingDuplicateConfirmationEnabled() || s.settingsPayload()[settingFindingDuplicateConfirmation] != false {
		t.Fatalf("disable setting: %d %s", w.Code, w.Body)
	}
	for _, name := range []string{"search_finding_duplicates", "merge_finding_records", "suggest_finding_merge"} {
		if _, err := s.performFindingCaseTool(context.Background(), name, a); err == nil {
			t.Fatalf("%s accepted while duplicate confirmation was off", name)
		}
	}

	w = httptest.NewRecorder()
	s.caseSuggestions(w, httptest.NewRequest("GET", fmt.Sprintf("/api/exploration/finding-case-suggestions?task_id=%d", task), nil))
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("disabled suggestions leaked: %d %s", w.Code, w.Body)
	}

	w = httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/exploration/finding-case-review", strings.NewReader(`{"finding_ids":[`+fmt.Sprint(ids[0])+`,`+fmt.Sprint(ids[1])+`]}`))
	s.reviewFindingCases(w, r)
	if w.Code != 409 {
		t.Fatalf("disabled manual review accepted: %d %s", w.Code, w.Body)
	}

	w = httptest.NewRecorder()
	s.putSettings(w, httptest.NewRequest("PUT", "/api/settings", strings.NewReader(`{"finding_duplicate_confirmation_enabled":true}`)))
	if w.Code != 200 {
		t.Fatalf("re-enable setting: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	s.caseSuggestions(w, httptest.NewRequest("GET", fmt.Sprintf("/api/exploration/finding-case-suggestions?task_id=%d", task), nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"left_id"`) {
		t.Fatalf("pending suggestion was deleted instead of hidden: %d %s", w.Code, w.Body)
	}
}
