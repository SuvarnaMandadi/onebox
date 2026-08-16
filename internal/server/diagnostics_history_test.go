package server

import (
	"context"
	"testing"
)

func TestRecordAndListDiagnosticsHistory(t *testing.T) {
	srv, db := newTestServer(t)
	_ = srv

	ok := providerDiagnosticsReport{Provider: "ollama", Status: severityOK, Version: "0.32.1", TotalCheckMS: 42}
	fail := providerDiagnosticsReport{Provider: "ollama", Status: severityError, TotalCheckMS: 7, Issues: []diagnosticIssue{{Summary: "connection refused"}}}

	if err := recordDiagnosticsRun(context.Background(), db, ok); err != nil {
		t.Fatalf("recordDiagnosticsRun(ok) error = %v", err)
	}
	if err := recordDiagnosticsRun(context.Background(), db, fail); err != nil {
		t.Fatalf("recordDiagnosticsRun(fail) error = %v", err)
	}

	entries, err := listDiagnosticsHistory(context.Background(), db, "ollama", 10)
	if err != nil {
		t.Fatalf("listDiagnosticsHistory() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	// Newest first.
	if entries[0].Success {
		t.Error("entries[0].Success = true, want false — the failed run was recorded most recently")
	}
	if entries[0].Summary != "connection refused" {
		t.Errorf("entries[0].Summary = %q, want %q", entries[0].Summary, "connection refused")
	}
	if !entries[1].Success {
		t.Error("entries[1].Success = false, want true")
	}
}

func TestListDiagnosticsHistoryFiltersByProvider(t *testing.T) {
	_, db := newTestServer(t)

	recordDiagnosticsRun(context.Background(), db, providerDiagnosticsReport{Provider: "ollama", Status: severityOK})
	recordDiagnosticsRun(context.Background(), db, providerDiagnosticsReport{Provider: "anthropic", Status: severityOK})

	entries, err := listDiagnosticsHistory(context.Background(), db, "anthropic", 10)
	if err != nil {
		t.Fatalf("listDiagnosticsHistory() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Provider != "anthropic" {
		t.Fatalf("entries = %+v, want exactly one anthropic entry", entries)
	}
}

func TestComputePerformanceSummaryEmptyHistory(t *testing.T) {
	_, db := newTestServer(t)
	summary, err := computePerformanceSummary(context.Background(), db, "ollama")
	if err != nil {
		t.Fatalf("computePerformanceSummary() error = %v", err)
	}
	if summary.SampleCount != 0 {
		t.Fatalf("SampleCount = %d, want 0", summary.SampleCount)
	}
	if summary.AvgResponseMS != nil {
		t.Error("AvgResponseMS should be nil (not a made-up 0) when there's no history")
	}
	if summary.HealthScore != 0 {
		t.Errorf("HealthScore = %d, want 0 for zero history — never a fake default", summary.HealthScore)
	}
}

func TestComputePerformanceSummaryAllSuccessful(t *testing.T) {
	_, db := newTestServer(t)
	for i := 0; i < 5; i++ {
		recordDiagnosticsRun(context.Background(), db, providerDiagnosticsReport{Provider: "ollama", Status: severityOK, TotalCheckMS: 100})
	}
	summary, err := computePerformanceSummary(context.Background(), db, "ollama")
	if err != nil {
		t.Fatalf("computePerformanceSummary() error = %v", err)
	}
	if summary.SuccessRatePercent == nil || *summary.SuccessRatePercent != 100 {
		t.Fatalf("SuccessRatePercent = %v, want 100", summary.SuccessRatePercent)
	}
	if summary.HealthScore != 100 {
		t.Errorf("HealthScore = %d, want 100 for a perfect recent record", summary.HealthScore)
	}
	if summary.LastFailureAt != "" {
		t.Error("LastFailureAt should be empty — every run succeeded")
	}
}

func TestComputePerformanceSummaryMixedResultsLowersScore(t *testing.T) {
	_, db := newTestServer(t)
	for i := 0; i < 3; i++ {
		recordDiagnosticsRun(context.Background(), db, providerDiagnosticsReport{Provider: "ollama", Status: severityError, TotalCheckMS: 50, Issues: []diagnosticIssue{{Summary: "failed"}}})
	}
	for i := 0; i < 2; i++ {
		recordDiagnosticsRun(context.Background(), db, providerDiagnosticsReport{Provider: "ollama", Status: severityOK, TotalCheckMS: 50})
	}
	summary, err := computePerformanceSummary(context.Background(), db, "ollama")
	if err != nil {
		t.Fatalf("computePerformanceSummary() error = %v", err)
	}
	if summary.HealthScore >= 100 {
		t.Fatalf("HealthScore = %d, want less than 100 — 3 of 5 recent runs failed", summary.HealthScore)
	}
	if len(summary.Warnings) == 0 {
		t.Error("expected at least one warning about the failure rate")
	}
}

func TestComputeHealthScoreNeverExceedsBounds(t *testing.T) {
	entries := []diagnosticsHistoryEntry{
		{Success: true, DurationMS: 10}, {Success: true, DurationMS: 10}, {Success: true, DurationMS: 10},
	}
	score, _ := computeHealthScore(100, 10, entries)
	if score < 0 || score > 100 {
		t.Fatalf("score = %d, want within [0, 100]", score)
	}
}
