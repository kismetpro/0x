package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

func TestFindingCaseGuidanceRespectsToolsAndTrafficSwitch(t *testing.T) {
	oldDuplicate := FindingDuplicateConfirmationEnabled
	FindingDuplicateConfirmationEnabled = func() bool { return true }
	t.Cleanup(func() { FindingDuplicateConfirmationEnabled = oldDuplicate })
	tools := []actool.CoreTool{}
	for _, name := range []string{"search_finding_duplicates", "merge_finding_records", "suggest_finding_merge", "get_finding_case", "update_finding_case_report"} {
		tools = append(tools, writeTool(name, name, obj(nil), func(context.Context, json.RawMessage) (actool.Result, error) { return actool.Text("ok"), nil }))
	}
	out, guide := findingCaseWorkflow("reporter", tools)
	if !strings.Contains(guide, "不能直接取成员最高等级") || !strings.Contains(guide, "独立报告") || !strings.Contains(guide, "人工确认") {
		t.Fatal("missing preservation rules")
	}
	for _, tool := range out {
		if tool.Name() == "merge_finding_records" {
			t.Fatal("automatic merge tool was exposed")
		}
	}
	if _, guide := findingCaseWorkflow("worker", tools); guide != "" {
		t.Fatal("worker changed")
	}
	if _, guide := findingCaseWorkflow("reporter", tools[:2]); guide != "" {
		t.Fatal("unbound tools reintroduced")
	}
	enabledGuide := guide
	system, _ := deferredSystem("CUSTOM PROMPT", DeferredInfo{CaseGuidance: enabledGuide})
	if !strings.HasPrefix(system[0], "CUSTOM PROMPT") || !strings.Contains(system[0], enabledGuide) {
		t.Fatal("custom prompt lost")
	}
	FindingDuplicateConfirmationEnabled = func() bool { return false }
	out, guide = findingCaseWorkflow("reporter", tools)
	if guide != "" || len(out) != 2 || out[0].Name() != "get_finding_case" || out[1].Name() != "update_finding_case_report" {
		t.Fatalf("disabled duplicate workflow not isolated: %v %q", out, guide)
	}
}

func TestHistoricalFindingReviewFiltersMutationAndProbeTools(t *testing.T) {
	oldAugment, oldResolve, oldDuplicate := ToolAugment, ToolResolve, FindingDuplicateConfirmationEnabled
	ToolAugment, ToolResolve = nil, nil
	FindingDuplicateConfirmationEnabled = func() bool { return true }
	t.Cleanup(func() {
		ToolAugment, ToolResolve, FindingDuplicateConfirmationEnabled = oldAugment, oldResolve, oldDuplicate
	})
	tools := []actool.CoreTool{}
	for _, name := range []string{"update_finding_report", "bind_finding_traffic", "report_finding", "Bash", "Write", "Edit", "mcp__custom__write", "get_finding_record", "merge_finding_records", "search_finding_duplicates", "suggest_finding_merge", "get_finding_case", "update_finding_case_report", "complete_finding_case_review"} {
		tools = append(tools, writeTool(name, name, obj(nil), func(context.Context, json.RawMessage) (actool.Result, error) { return actool.Text("ok"), nil }))
	}
	out, def, cleanup := AugmentTools(WithFindingCaseReview(t.Context()), "reporter", tools)
	defer cleanup()
	for _, tool := range out {
		switch tool.Name() {
		case "update_finding_report", "bind_finding_traffic", "report_finding", "Bash", "Write", "Edit", "mcp__custom__write", "merge_finding_records":
			t.Fatal("historical mutation capability", tool.Name())
		}
	}
	if len(out) != 6 || def.FindingGuidance != "" || !strings.Contains(def.CaseGuidance, "历史整理模式") || !strings.Contains(def.CaseGuidance, "接口、路径、后端、读/写影响、评级不同") {
		t.Fatal("missing historical isolation", def)
	}
	FindingDuplicateConfirmationEnabled = func() bool { return false }
	out, def, cleanupOff := AugmentTools(WithFindingCaseReview(t.Context()), "reporter", tools)
	defer cleanupOff()
	if len(out) != 4 || !strings.Contains(def.CaseGuidance, "漏洞重复确认已关闭") || strings.Contains(def.CaseGuidance, "suggest_finding_merge") {
		t.Fatal("disabled historical review retained duplicate workflow", def)
	}
	normal, _, cl := AugmentTools(t.Context(), "reporter", tools)
	defer cl()
	found := map[string]bool{}
	for _, tool := range normal {
		found[tool.Name()] = true
	}
	if !found["update_finding_report"] || !found["Bash"] || !found["report_finding"] {
		t.Fatal("normal reporting was restricted")
	}
}
