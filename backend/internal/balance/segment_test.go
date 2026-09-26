package balance

import (
	"testing"
	"time"
)

func segmentTimeline() []SegmentSnapshotInput {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	return []SegmentSnapshotInput{
		{SnapshotID: 1, MeasuredAt: base, MassKG: 55_000_000, UncertaintyPct: 0.10},
		{SnapshotID: 2, MeasuredAt: base.Add(8 * time.Hour), MassKG: 55_250_000, UncertaintyPct: 0.10},
		{SnapshotID: 3, MeasuredAt: base.Add(16 * time.Hour), MassKG: 55_100_000, UncertaintyPct: 0.12},
	}
}

func TestReconcileSegmentsAttributesContainedTransfers(t *testing.T) {
	snapshots := segmentTimeline()
	base := snapshots[0].MeasuredAt
	transfers := []SegmentTransferInput{
		{TransferID: 20, StartAt: base.Add(time.Hour), EndAt: base.Add(2 * time.Hour), SignedMassKG: 250000, UncertaintyPct: 0.20},
		{TransferID: 21, StartAt: base.Add(9 * time.Hour), EndAt: base.Add(10 * time.Hour), SignedMassKG: -150000, UncertaintyPct: 0.25},
	}
	segments, unallocated, err := ReconcileSegments(snapshots, transfers)
	if err != nil {
		t.Fatalf("reconcile segments: %v", err)
	}
	if len(segments) != 2 || len(unallocated) != 0 {
		t.Fatalf("unexpected reconciliation shape: %d segments, %d unallocated", len(segments), len(unallocated))
	}
	first, second := segments[0], segments[1]
	if first.Index != 1 || first.OpeningSnapshotID != 1 || first.ClosingSnapshotID != 2 {
		t.Fatalf("unexpected first segment identity: %+v", first)
	}
	if first.TransferCount != 1 || first.NetTransferKG != 250000 || first.MassChangeKG != 250000 {
		t.Fatalf("unexpected first segment totals: %+v", first)
	}
	if first.UnexplainedKG != 0 || first.ExceedsUncertainty {
		t.Fatalf("first segment should reconcile exactly: %+v", first)
	}
	if second.TransferCount != 1 || second.NetTransferKG != -150000 || second.UnexplainedKG != 0 {
		t.Fatalf("unexpected second segment totals: %+v", second)
	}
	if !second.StartAt.Equal(snapshots[1].MeasuredAt) || !second.EndAt.Equal(snapshots[2].MeasuredAt) {
		t.Fatalf("segment must expose start and end times: %+v", second)
	}
	if second.UncertaintyKG <= 0 {
		t.Fatalf("segment uncertainty must be propagated: %+v", second)
	}
}

func TestReconcileSegmentsFlagsMissedTransfer(t *testing.T) {
	snapshots := segmentTimeline()
	segments, _, err := ReconcileSegments(snapshots, nil)
	if err != nil {
		t.Fatalf("reconcile segments: %v", err)
	}
	first, second := segments[0], segments[1]
	// 250 t of mass appeared in the first segment without any confirmed
	// transfer; the second segment lost 150 t equally unexplained.
	if !first.ExceedsUncertainty || first.UnexplainedKG != -250000 {
		t.Fatalf("first segment must flag the missed inflow: %+v", first)
	}
	if !second.ExceedsUncertainty || second.UnexplainedKG != 150000 {
		t.Fatalf("second segment must flag the missed outflow: %+v", second)
	}
}

func TestReconcileSegmentsValidatesTimeline(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	if _, _, err := ReconcileSegments([]SegmentSnapshotInput{
		{SnapshotID: 1, MeasuredAt: base, MassKG: 1000, UncertaintyPct: 0.3},
	}, nil); err == nil {
		t.Fatal("expected single-snapshot chain to fail")
	}
	if _, _, err := ReconcileSegments([]SegmentSnapshotInput{
		{SnapshotID: 1, MeasuredAt: base, MassKG: 1000, UncertaintyPct: 0.3},
		{SnapshotID: 2, MeasuredAt: base, MassKG: 1000, UncertaintyPct: 0.3},
	}, nil); err == nil {
		t.Fatal("expected non-increasing timeline to fail")
	}
	if _, _, err := ReconcileSegments([]SegmentSnapshotInput{
		{SnapshotID: 1, MeasuredAt: base, MassKG: -5, UncertaintyPct: 0.3},
		{SnapshotID: 2, MeasuredAt: base.Add(time.Hour), MassKG: 1000, UncertaintyPct: 0.3},
	}, nil); err == nil {
		t.Fatal("expected negative snapshot mass to fail")
	}
}
