package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kismetpro/0x/db"
)

func TestFindingNumbersInEveryReportFormat(t *testing.T) {
	for _, f := range []*db.DBFinding{
		{ID: 42, Name: "verified local issue", Severity: "high", Report: "proof"},
		{ID: 42, ExportCaseID: 9, Name: "unified issue", Severity: "medium", Report: "proof"},
	} {
		number := f.Number()
		for name, body := range map[string]string{
			"single":   SingleFindingMarkdown(f, time.Now()),
			"summary":  FindingsMarkdown([]*db.DBFinding{f}, time.Now()),
			"csv":      string(FindingsCSV([]*db.DBFinding{f})),
			"filename": FindingFilename(f),
		} {
			if !strings.Contains(body, number) {
				t.Errorf("%s missing %s", name, number)
			}
		}
		f.Name, f.Severity = "renamed", "low"
		if number != f.Number() {
			t.Fatal("editing changed immutable number")
		}
	}
}

func TestFindingFilenameRetainsNumberAndValidUTF8(t *testing.T) {
	f := &db.DBFinding{ID: 123456789, Name: strings.Repeat("漏洞/../", 100), Severity: "critical"}
	name := FindingFilename(f)
	if !utf8.ValidString(name) || len(name) > 123 || strings.ContainsAny(name, "/\\") || !strings.HasPrefix(name, f.Number()) {
		t.Fatalf("unsafe filename: %q", name)
	}
}

func TestTaskReportUsesStandaloneNumberNotNodeID(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"name": "local test", "severity": "high", "summary": "proof"})
	md := Markdown(Input{Findings: []*db.Node{{ID: 200, Payload: payload}}, FindingNumbers: map[int64]string{200: db.FindingNumber(42)}})
	if !strings.Contains(md, "0X-V-00000042") || strings.Contains(md, "0X-V-00000200") {
		t.Fatal("task/global report identities diverged")
	}
}
