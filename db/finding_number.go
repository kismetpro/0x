package db

import "fmt"

// Numbers use immutable database identities, never row positions or severity.
// Separate namespaces keep original observations and consolidated defects distinct.
func FindingNumber(id int64) string {
	if id <= 0 {
		return ""
	}
	return fmt.Sprintf("0X-V-%08d", id)
}

func FindingCaseNumber(id int64) string {
	if id <= 0 {
		return ""
	}
	return fmt.Sprintf("0X-C-%08d", id)
}

func (f *DBFinding) Number() string {
	if f.ExportCaseID > 0 {
		return FindingCaseNumber(f.ExportCaseID)
	}
	return FindingNumber(f.ID)
}
