package balance

import (
	"testing"
	"time"

	"lng-boiloff-gas-balance/backend/internal/constants"
)

func TestReconcileSegmentsOrdersChainAndAttributesTransfers(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	snapshotA := SegmentSnapshotInput{ID: 1, MeasuredAt: base, MassKG: 1000, UncertaintyPct: 1}
	snapshotB := SegmentSnapshotInput{ID: 2, MeasuredAt: base.Add(10 * time.Hour), MassKG: 990, UncertaintyPct: 1}
	snapshotC := SegmentSnapshotInput{ID: 3, MeasuredAt: base.Add(20 * time.Hour), MassKG: 1005, UncertaintyPct: 1}
	// 故意打乱输入顺序，函数必须按时间重排。
	result, err := ReconcileSegments(
		[]SegmentSnapshotInput{snapshotC, snapshotA, snapshotB},
		[]SegmentTransferInput{
			{ID: 10, OperationType: "inflow", StartAt: base.Add(12 * time.Hour), MassKG: 25, UncertaintyPct: 1},
			{ID: 11, OperationType: "outflow", StartAt: base.Add(15 * time.Hour), MassKG: 5, UncertaintyPct: 1},
			{ID: 12, OperationType: "inflow", StartAt: base.Add(22 * time.Hour), MassKG: 3, UncertaintyPct: 1},
		},
	)
	if err != nil {
		t.Fatalf("reconcile segments: %v", err)
	}
	if len(result.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(result.Segments))
	}
	first, second := result.Segments[0], result.Segments[1]
	if first.StartAt != snapshotA.MeasuredAt || first.EndAt != snapshotB.MeasuredAt {
		t.Fatalf("first segment boundaries not time ordered: %+v", first)
	}
	if first.NetTransferKG != 0 || len(first.TransferIDs) != 0 || first.DiscrepancyKG != 10 {
		t.Fatalf("first segment should have no transfers and a 10 kg gap: %+v", first)
	}
	if first.Unexplained {
		t.Fatalf("first segment discrepancy must stay inside combined uncertainty")
	}
	if second.NetTransferKG != 20 || len(second.TransferIDs) != 2 || second.DiscrepancyKG != 5 {
		t.Fatalf("second segment should net 20 kg from both transfers: %+v", second)
	}
	if second.Level != constants.DeviationWithinUncertainty {
		t.Fatalf("second segment should classify within uncertainty, got %s", second.Level)
	}
	if len(result.OutsideChainTransferIDs) != 1 || result.OutsideChainTransferIDs[0] != 12 {
		t.Fatalf("transfer starting after the final snapshot must stay outside the chain: %+v", result.OutsideChainTransferIDs)
	}
}

func TestReconcileSegmentsFlagsUnexplainedInterval(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	result, err := ReconcileSegments(
		[]SegmentSnapshotInput{
			{ID: 1, MeasuredAt: base, MassKG: 1000, UncertaintyPct: 0.1},
			{ID: 2, MeasuredAt: base.Add(time.Hour), MassKG: 950, UncertaintyPct: 0.1},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("reconcile segments: %v", err)
	}
	segment := result.Segments[0]
	if segment.DiscrepancyKG != 50 {
		t.Fatalf("unexpected segment discrepancy %.3f", segment.DiscrepancyKG)
	}
	if segment.UncertaintyKG <= 0 || segment.DiscrepancyKG <= segment.UncertaintyKG {
		t.Fatalf("test setup should exceed combined uncertainty, got U=%.3f D=%.3f", segment.UncertaintyKG, segment.DiscrepancyKG)
	}
	if !segment.Unexplained {
		t.Fatal("segment with discrepancy beyond combined uncertainty must be unexplained")
	}
	if segment.Level != constants.DeviationInvestigate {
		t.Fatalf("expected investigate level, got %s", segment.Level)
	}
}

func TestReconcileSegmentsRejectsInvalidChains(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	if _, err := ReconcileSegments(nil, nil); err == nil {
		t.Fatal("expected error when fewer than two snapshots are supplied")
	}
	duplicate := []SegmentSnapshotInput{
		{ID: 1, MeasuredAt: base, MassKG: 1000, UncertaintyPct: 1},
		{ID: 2, MeasuredAt: base, MassKG: 1000, UncertaintyPct: 1},
	}
	if _, err := ReconcileSegments(duplicate, nil); err == nil {
		t.Fatal("expected error for snapshots with non-increasing measurement times")
	}
}
