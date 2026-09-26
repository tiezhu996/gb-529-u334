package service

import (
	"encoding/json"
	"testing"
	"time"

	"gorm.io/datatypes"

	"lng-boiloff-gas-balance/backend/internal/balance"
	"lng-boiloff-gas-balance/backend/internal/constants"
	"lng-boiloff-gas-balance/backend/internal/model"
)

func TestCalculateBalanceRunProducesReplayEvidence(t *testing.T) {
	curve, _ := balance.NewCapacityCurve([]float64{0, 15000})
	raw, _ := curve.Marshal()
	tank := model.StorageTank{
		ID: 1, TankCode: "TK-TEST", NominalCapacityM3: 180000, MinLevelM: 0, MaxLevelM: 12,
		ReferenceDensityKGM3: 452, ReferenceTemperatureC: -160, ThermalExpansionPerC: 0.0035,
		CapacityCurveJSON: datatypes.JSON(raw), CoefficientVersion: "CV-T1", TankStatus: "active",
	}
	start := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	opening := model.MeasurementSnapshot{
		ID: 10, TankID: 1, MeasuredAt: start.Add(-time.Hour), CalculatedLiquidMassKG: 55_000_000,
		MeasurementUncertaintyPct: 0.30, QualityFlag: constants.QualityGood,
	}
	closing := model.MeasurementSnapshot{
		ID: 11, TankID: 1, MeasuredAt: end.Add(-time.Hour), CalculatedLiquidMassKG: 55_100_000,
		MeasurementUncertaintyPct: 0.32, QualityFlag: constants.QualityGood,
	}
	transfers := []model.TransferOperation{
		{ID: 20, OperationType: "inflow", StartAt: start.Add(2 * time.Hour), EndAt: start.Add(3 * time.Hour), MeasuredMassKG: 250000, MeasurementUncertaintyPct: 0.2},
		{ID: 21, OperationType: "outflow", StartAt: start.Add(10 * time.Hour), EndAt: start.Add(11 * time.Hour), MeasuredMassKG: 90000, MeasurementUncertaintyPct: 0.25},
	}
	calculated, snapshot, evidence, err := calculateBalanceRun(tank, opening, closing, nil, transfers, start, end)
	if err != nil {
		t.Fatalf("calculate run: %v", err)
	}
	if calculated.NetTransferKG != 160000 || calculated.EstimatedBOGKG != 60000 {
		t.Fatalf("unexpected equation result: %+v", calculated)
	}
	if len(snapshot) < 200 || len(evidence) < 200 {
		t.Fatalf("expected replay snapshot and evidence, got %d/%d bytes", len(snapshot), len(evidence))
	}
	if calculated.DeviationLevel != constants.DeviationWithinUncertainty {
		t.Fatalf("unexpected deviation level: %s", calculated.DeviationLevel)
	}
	var decoded struct {
		Segments segmentEvidence `json:"segments"`
	}
	if err := json.Unmarshal(evidence, &decoded); err != nil {
		t.Fatalf("decode segment evidence: %v", err)
	}
	if decoded.Segments.SegmentCount != 1 || len(decoded.Segments.Items) != 1 {
		t.Fatalf("expected one boundary-only segment, got %+v", decoded.Segments)
	}
	if decoded.Segments.Items[0].TransferCount != 2 || decoded.Segments.Items[0].UnexplainedKG != 60000 {
		t.Fatalf("unexpected segment reconciliation: %+v", decoded.Segments.Items[0])
	}
}

func TestCalculateBalanceRunFlagsUnexplainedSegment(t *testing.T) {
	curve, _ := balance.NewCapacityCurve([]float64{0, 15000})
	raw, _ := curve.Marshal()
	tank := model.StorageTank{
		ID: 1, TankCode: "TK-TEST", NominalCapacityM3: 180000, MinLevelM: 0, MaxLevelM: 12,
		ReferenceDensityKGM3: 452, ReferenceTemperatureC: -160, ThermalExpansionPerC: 0.0035,
		CapacityCurveJSON: datatypes.JSON(raw), CoefficientVersion: "CV-T1", TankStatus: "active",
	}
	start := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	opening := model.MeasurementSnapshot{
		ID: 10, TankID: 1, MeasuredAt: start.Add(-time.Hour), CalculatedLiquidMassKG: 55_000_000,
		MeasurementUncertaintyPct: 0.30, QualityFlag: constants.QualityGood,
	}
	middle := model.MeasurementSnapshot{
		ID: 12, TankID: 1, MeasuredAt: start.Add(12 * time.Hour), CalculatedLiquidMassKG: 55_250_000,
		MeasurementUncertaintyPct: 0.30, QualityFlag: constants.QualityGood,
	}
	closing := model.MeasurementSnapshot{
		ID: 11, TankID: 1, MeasuredAt: end.Add(-time.Hour), CalculatedLiquidMassKG: 55_700_000,
		MeasurementUncertaintyPct: 0.32, QualityFlag: constants.QualityGood,
	}
	// Only the first inflow is confirmed; the second segment gained 450 t of
	// mass with no matching transfer, which must surface as unexplained.
	transfers := []model.TransferOperation{
		{ID: 20, OperationType: "inflow", StartAt: start.Add(time.Hour), EndAt: start.Add(2 * time.Hour), MeasuredMassKG: 250000, MeasurementUncertaintyPct: 0.2},
	}
	_, _, evidence, err := calculateBalanceRun(tank, opening, closing, []model.MeasurementSnapshot{middle}, transfers, start, end)
	if err != nil {
		t.Fatalf("calculate run: %v", err)
	}
	var decoded struct {
		Segments segmentEvidence `json:"segments"`
	}
	if err := json.Unmarshal(evidence, &decoded); err != nil {
		t.Fatalf("decode segment evidence: %v", err)
	}
	segments := decoded.Segments
	if segments.SnapshotCount != 3 || segments.SegmentCount != 2 || len(segments.Items) != 2 {
		t.Fatalf("expected two segments from three snapshots, got %+v", segments)
	}
	first, second := segments.Items[0], segments.Items[1]
	if first.ExceedsUncertainty {
		t.Fatalf("first segment should reconcile within uncertainty: %+v", first)
	}
	if !second.ExceedsUncertainty || segments.UnexplainedCount != 1 {
		t.Fatalf("second segment must be flagged unexplained: %+v", segments)
	}
	if second.UnexplainedKG != -450000 {
		t.Fatalf("unexpected unexplained mass in second segment: %.3f", second.UnexplainedKG)
	}
	if !second.StartAt.Equal(middle.MeasuredAt) || !second.EndAt.Equal(closing.MeasuredAt) {
		t.Fatalf("segment boundaries must expose start and end times: %+v", second)
	}
}

func TestReconcileSegmentsLeavesCrossingTransferUnallocated(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	snapshots := []balance.SegmentSnapshotInput{
		{SnapshotID: 1, MeasuredAt: base, MassKG: 55_000_000, UncertaintyPct: 0.3},
		{SnapshotID: 2, MeasuredAt: base.Add(12 * time.Hour), MassKG: 55_050_000, UncertaintyPct: 0.3},
		{SnapshotID: 3, MeasuredAt: base.Add(24 * time.Hour), MassKG: 55_000_000, UncertaintyPct: 0.3},
	}
	transfers := []balance.SegmentTransferInput{
		{TransferID: 7, StartAt: base.Add(6 * time.Hour), EndAt: base.Add(18 * time.Hour), SignedMassKG: 50000, UncertaintyPct: 0.2},
	}
	segments, unallocated, err := balance.ReconcileSegments(snapshots, transfers)
	if err != nil {
		t.Fatalf("reconcile segments: %v", err)
	}
	if len(segments) != 2 {
		t.Fatalf("expected two segments, got %d", len(segments))
	}
	for _, segment := range segments {
		if segment.TransferCount != 0 || segment.NetTransferKG != 0 {
			t.Fatalf("crossing transfer must not be attributed: %+v", segment)
		}
	}
	if len(unallocated) != 1 || unallocated[0].TransferID != 7 {
		t.Fatalf("expected crossing transfer to be listed unallocated: %+v", unallocated)
	}
}

func TestBalanceStateMachine(t *testing.T) {
	valid := []struct {
		from constants.BalanceStatus
		to   constants.BalanceStatus
	}{
		{constants.BalanceQueued, constants.BalanceCalculating},
		{constants.BalanceCalculating, constants.BalancePendingReview},
		{constants.BalancePendingReview, constants.BalanceAccepted},
		{constants.BalancePendingReview, constants.BalanceRejected},
	}
	for _, transition := range valid {
		if !constants.CanTransitionBalance(transition.from, transition.to) {
			t.Fatalf("expected valid transition %s -> %s", transition.from, transition.to)
		}
	}
	if constants.CanTransitionBalance(constants.BalanceAccepted, constants.BalanceCalculating) {
		t.Fatal("accepted result must remain immutable")
	}
}
