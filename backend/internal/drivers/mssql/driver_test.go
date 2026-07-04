package mssql

import "testing"

func TestExplainSettings(t *testing.T) {
	on, off := explainSettings(false)
	if on != "SET SHOWPLAN_XML ON" || off != "SET SHOWPLAN_XML OFF" {
		t.Fatalf("explainSettings(analyze=false): on=%q off=%q", on, off)
	}

	on, off = explainSettings(true)
	if on != "SET STATISTICS XML ON" || off != "SET STATISTICS XML OFF" {
		t.Fatalf("explainSettings(analyze=true): on=%q off=%q", on, off)
	}
}
