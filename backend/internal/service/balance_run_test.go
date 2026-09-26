package service

import (
	"encoding/json"
	"testing"
	"time"

	"gorm.io/datatypes"

	"lng-boiloff-gas-balance/backend/internal/balance"
	"lng-boiloff-gas-balance/backend/internal/constants"
	"lng-boiloff-gas-balance/backend/internal/dto"
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
		{ID: 20, OperationType: "inflow", MeasuredMassKG: 250000, MeasurementUncertaintyPct: 0.2},
		{ID: 21, OperationType: "outflow", MeasuredMassKG: 90000, MeasurementUncertaintyPct: 0.25},
	}
	chain := []model.MeasurementSnapshot{opening, closing}
	calculated, snapshot, evidence, err := calculateBalanceRun(tank, opening, closing, chain, transfers, start, end)
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
}

func TestCalculateBalanceRunReconcilesIntervalSegments(t *testing.T) {
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
	intermediate := model.MeasurementSnapshot{
		ID: 12, TankID: 1, MeasuredAt: start.Add(8 * time.Hour), CalculatedLiquidMassKG: 54_500_000,
		MeasurementUncertaintyPct: 0.30, QualityFlag: constants.QualityGood,
	}
	closing := model.MeasurementSnapshot{
		ID: 11, TankID: 1, MeasuredAt: end.Add(-time.Hour), CalculatedLiquidMassKG: 55_100_000,
		MeasurementUncertaintyPct: 0.32, QualityFlag: constants.QualityGood,
	}
	chain := []model.MeasurementSnapshot{opening, intermediate, closing}
	transfers := []model.TransferOperation{
		{ID: 20, OperationType: "inflow", StartAt: start.Add(10 * time.Hour), MeasuredMassKG: 850000, MeasurementUncertaintyPct: 0.2},
		{ID: 21, OperationType: "outflow", StartAt: start.Add(12 * time.Hour), MeasuredMassKG: 90000, MeasurementUncertaintyPct: 0.25},
	}
	calculated, snapshot, evidence, err := calculateBalanceRun(tank, opening, closing, chain, transfers, start, end)
	if err != nil {
		t.Fatalf("calculate run: %v", err)
	}
	if calculated.NetTransferKG != 760000 || calculated.EstimatedBOGKG != 660000 {
		t.Fatalf("unexpected equation result: %+v", calculated)
	}
	var decoded struct {
		Segments             []dto.BalanceSegment `json:"segments"`
		UnexplainedIntervals []dto.BalanceSegment `json:"unexplained_intervals"`
	}
	if err := json.Unmarshal(evidence, &decoded); err != nil {
		t.Fatalf("decode evidence: %v", err)
	}
	if len(decoded.Segments) != 2 {
		t.Fatalf("expected 2 interval segments, got %d", len(decoded.Segments))
	}
	first, second := decoded.Segments[0], decoded.Segments[1]
	if first.StartAt != opening.MeasuredAt || first.EndAt != intermediate.MeasuredAt {
		t.Fatalf("first segment boundaries wrong: %+v", first)
	}
	if first.DiscrepancyKG != 500000 || !first.Unexplained {
		t.Fatalf("first segment should be an unexplained 500000 kg gap: %+v", first)
	}
	if second.DiscrepancyKG != 160000 || second.Unexplained {
		t.Fatalf("second segment should stay within uncertainty: %+v", second)
	}
	if second.NetTransferKG != 760000 || len(second.TransferIDs) != 2 {
		t.Fatalf("second segment should carry both confirmed transfers: %+v", second)
	}
	if first.DiscrepancyKG+second.DiscrepancyKG != calculated.EstimatedBOGKG {
		t.Fatalf("segment discrepancies must sum to the overall deviation")
	}
	if len(decoded.UnexplainedIntervals) != 1 || decoded.UnexplainedIntervals[0].Sequence != 1 {
		t.Fatalf("expected exactly the first segment as unexplained interval: %+v", decoded.UnexplainedIntervals)
	}
	var input struct {
		IntervalSnapshots []model.MeasurementSnapshot `json:"interval_snapshots"`
	}
	if err := json.Unmarshal(snapshot, &input); err != nil {
		t.Fatalf("decode input snapshot: %v", err)
	}
	if len(input.IntervalSnapshots) != 3 {
		t.Fatalf("expected interval snapshot chain in replay evidence, got %d", len(input.IntervalSnapshots))
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
