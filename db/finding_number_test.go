package db

import (
	"strings"
	"testing"
)

func TestFindingNumberNamespacesAndSearch(t *testing.T) {
	if FindingNumber(42) != "0X-V-00000042" || FindingCaseNumber(42) != "0X-C-00000042" {
		t.Fatal("unstable number")
	}
	if FindingNumber(123456789) != "0X-V-123456789" {
		t.Fatal("large ID truncated")
	}
	for _, tc := range []struct{ query, fragment string }{
		{"0x-v-00000042", "f.id=$2"}, {"0X-C-00000042", "m.case_id=$2"},
	} {
		where, args := (FindingFilter{Severity: "high", Query: tc.query}).where()
		if !strings.Contains(where, tc.fragment) || len(args) != 2 || args[1] != int64(42) {
			t.Fatalf("search %s: %s %v", tc.query, where, args)
		}
	}
	where, args := (FindingFilter{Query: "0X-V-1 OR 1=1"}).where()
	if strings.Contains(where, "1 OR 1=1") || len(args) != 1 {
		t.Fatal("unsafe number search")
	}
}
