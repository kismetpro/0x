package server

import (
	"testing"

	"github.com/kismetpro/0x/db"
)

func TestFindingsWithDetailedReportsOnlyKeepsGeneratedReports(t *testing.T) {
	taskID := int64(42)
	fs := []*db.DBFinding{
		{ID: 1, TaskID: &taskID, TaskName: "客户门户", Report: "完整报告"},
		{ID: 2, TaskID: &taskID, TaskName: "客户门户", Report: " \n\t "},
		nil,
	}
	got := findingsWithDetailedReports(fs)
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("unexpected export set: %+v", got)
	}
	dto := findingFromDB(got[0], nil)
	if dto.TaskName != "客户门户" || dto.TaskID != "42" {
		t.Fatalf("task identity missing from JSON DTO: %+v", dto)
	}
}
