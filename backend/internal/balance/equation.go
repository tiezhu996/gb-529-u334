package balance

import (
	"fmt"
	"math"
	"time"
)

type SnapshotMassInput struct {
	LevelM                float64
	MinimumLevelM         float64
	MaximumLevelM         float64
	NominalCapacityM3     float64
	DensityKGM3           float64
	TemperatureC          float64
	ReferenceTemperatureC float64
	ThermalExpansionPerC  float64
	Curve                 CapacityCurve
}

type SnapshotMassResult struct {
	VolumeM3             float64 `json:"volume_m3"`
	CorrectedDensityKGM3 float64 `json:"corrected_density_kgm3"`
	LiquidMassKG         float64 `json:"liquid_mass_kg"`
}

func CorrectDensity(density, temperature, referenceTemperature, expansion float64) (float64, error) {
	if err := ValidateDensity(density); err != nil {
		return 0, err
	}
	if err := ValidateTemperature(temperature); err != nil {
		return 0, err
	}
	if expansion < 0 || expansion > 0.01 || !finite(expansion) {
		return 0, fmt.Errorf("thermal expansion coefficient %.8f is outside [0, 0.01]", expansion)
	}
	denominator := 1 + expansion*(temperature-referenceTemperature)
	if denominator <= 0.5 || denominator >= 1.5 {
		return 0, fmt.Errorf("temperature correction denominator %.6f is not physically usable", denominator)
	}
	corrected := density / denominator
	if !finite(corrected) || corrected <= 0 {
		return 0, fmt.Errorf("temperature correction produced an invalid density")
	}
	return Round(corrected, 6), nil
}

func CalculateSnapshotMass(input SnapshotMassInput) (SnapshotMassResult, error) {
	volume, err := input.Curve.VolumeAt(input.LevelM, input.MinimumLevelM, input.MaximumLevelM, input.NominalCapacityM3)
	if err != nil {
		return SnapshotMassResult{}, fmt.Errorf("calculate calibrated volume: %w", err)
	}
	density, err := CorrectDensity(input.DensityKGM3, input.TemperatureC, input.ReferenceTemperatureC, input.ThermalExpansionPerC)
	if err != nil {
		return SnapshotMassResult{}, fmt.Errorf("calculate temperature-corrected density: %w", err)
	}
	mass := volume * density
	if !finite(mass) || mass < 0 {
		return SnapshotMassResult{}, fmt.Errorf("calculated liquid mass is invalid")
	}
	return SnapshotMassResult{VolumeM3: volume, CorrectedDensityKGM3: density, LiquidMassKG: Round(mass, 3)}, nil
}

func NetTransfer(inflows, outflows []float64) (float64, error) {
	total := 0.0
	for _, value := range inflows {
		if value < 0 || !finite(value) {
			return 0, fmt.Errorf("inflow mass must be finite and non-negative")
		}
		total += value
	}
	for _, value := range outflows {
		if value < 0 || !finite(value) {
			return 0, fmt.Errorf("outflow mass must be finite and non-negative")
		}
		total -= value
	}
	return Round(total, 3), nil
}

func PhysicalBalance(openingMass, netTransfer, closingMass float64) (float64, error) {
	if openingMass < 0 || closingMass < 0 || !finite(openingMass) || !finite(closingMass) || !finite(netTransfer) {
		return 0, fmt.Errorf("mass balance inputs must be finite and boundary masses non-negative")
	}
	return Round(openingMass+netTransfer-closingMass, 3), nil
}

func DeviationPercent(deviation, openingMass float64) float64 {
	if math.Abs(openingMass) < 1e-9 {
		return 0
	}
	return Round(deviation/openingMass*100, 6)
}

// SegmentSnapshotInput is one effective (good/suspect) snapshot placed on the
// intra-period reconciliation timeline.
type SegmentSnapshotInput struct {
	SnapshotID     uint
	MeasuredAt     time.Time
	MassKG         float64
	UncertaintyPct float64
}

// SegmentTransferInput is a confirmed transfer offered to segment allocation.
// SignedMassKG is positive for inflow and negative for outflow.
type SegmentTransferInput struct {
	TransferID     uint
	StartAt        time.Time
	EndAt          time.Time
	SignedMassKG   float64
	UncertaintyPct float64
}

// SegmentReconciliation compares the observed mass change between two adjacent
// snapshots with the net confirmed transfer fully contained in that interval.
type SegmentReconciliation struct {
	Index              int       `json:"index"`
	OpeningSnapshotID  uint      `json:"opening_snapshot_id"`
	ClosingSnapshotID  uint      `json:"closing_snapshot_id"`
	StartAt            time.Time `json:"start_at"`
	EndAt              time.Time `json:"end_at"`
	MassChangeKG       float64   `json:"mass_change_kg"`
	NetTransferKG      float64   `json:"net_transfer_kg"`
	TransferCount      int       `json:"transfer_count"`
	UnexplainedKG      float64   `json:"unexplained_kg"`
	UncertaintyKG      float64   `json:"uncertainty_kg"`
	ExceedsUncertainty bool      `json:"exceeds_uncertainty"`
}

// UnallocatedTransfer names a confirmed transfer that cannot be attributed to
// a single segment, for example one crossing an intermediate snapshot time.
type UnallocatedTransfer struct {
	TransferID uint   `json:"transfer_id"`
	Reason     string `json:"reason"`
}

// ReconcileSegments sorts nothing itself: callers supply snapshots strictly
// ascending by measured time. A transfer is attributed to a segment only when
// its whole [start_at, end_at] interval lies within the segment boundaries.
// Per-segment unexplained deviation follows the overall sign convention,
// opening mass + net transfer - closing mass, and is flagged when its absolute
// value exceeds the combined uncertainty propagated for that segment.
func ReconcileSegments(snapshots []SegmentSnapshotInput, transfers []SegmentTransferInput) ([]SegmentReconciliation, []UnallocatedTransfer, error) {
	if len(snapshots) < 2 {
		return nil, nil, fmt.Errorf("segment reconciliation requires at least two boundary snapshots")
	}
	for index, snapshot := range snapshots {
		if snapshot.MassKG < 0 || !finite(snapshot.MassKG) {
			return nil, nil, fmt.Errorf("segment snapshot %d mass is invalid", index)
		}
		if err := ValidateUncertainty(snapshot.UncertaintyPct); err != nil {
			return nil, nil, fmt.Errorf("segment snapshot %d: %w", index, err)
		}
		if !snapshot.MeasuredAt.IsZero() && index > 0 && !snapshot.MeasuredAt.After(snapshots[index-1].MeasuredAt) {
			return nil, nil, fmt.Errorf("segment snapshot timeline must be strictly increasing")
		}
	}
	attributed := make([]bool, len(transfers))
	segments := make([]SegmentReconciliation, 0, len(snapshots)-1)
	for index := 0; index < len(snapshots)-1; index++ {
		openingSnap, closingSnap := snapshots[index], snapshots[index+1]
		netTransfer := 0.0
		transferCount := 0
		uncertaintyInputs := []UncertaintyInput{
			{Source: "segment_opening_snapshot", EntityID: openingSnap.SnapshotID, MassKG: openingSnap.MassKG, UncertaintyPct: openingSnap.UncertaintyPct},
			{Source: "segment_closing_snapshot", EntityID: closingSnap.SnapshotID, MassKG: closingSnap.MassKG, UncertaintyPct: closingSnap.UncertaintyPct},
		}
		for transferIndex, transfer := range transfers {
			if attributed[transferIndex] || transfer.StartAt.Before(openingSnap.MeasuredAt) || transfer.EndAt.After(closingSnap.MeasuredAt) {
				continue
			}
			if !finite(transfer.SignedMassKG) {
				return nil, nil, fmt.Errorf("segment transfer %d signed mass is invalid", transfer.TransferID)
			}
			if err := ValidateUncertainty(transfer.UncertaintyPct); err != nil {
				return nil, nil, fmt.Errorf("segment transfer %d: %w", transfer.TransferID, err)
			}
			attributed[transferIndex] = true
			transferCount++
			netTransfer += transfer.SignedMassKG
			uncertaintyInputs = append(uncertaintyInputs, UncertaintyInput{
				Source:         "segment_transfer",
				EntityID:       transfer.TransferID,
				MassKG:         math.Abs(transfer.SignedMassKG),
				UncertaintyPct: transfer.UncertaintyPct,
			})
		}
		propagated, err := PropagateUncertainty(uncertaintyInputs)
		if err != nil {
			return nil, nil, fmt.Errorf("propagate segment %d uncertainty: %w", index+1, err)
		}
		unexplained, err := PhysicalBalance(openingSnap.MassKG, netTransfer, closingSnap.MassKG)
		if err != nil {
			return nil, nil, fmt.Errorf("calculate segment %d mass balance: %w", index+1, err)
		}
		segments = append(segments, SegmentReconciliation{
			Index:              index + 1,
			OpeningSnapshotID:  openingSnap.SnapshotID,
			ClosingSnapshotID:  closingSnap.SnapshotID,
			StartAt:            openingSnap.MeasuredAt,
			EndAt:              closingSnap.MeasuredAt,
			MassChangeKG:       Round(closingSnap.MassKG-openingSnap.MassKG, 3),
			NetTransferKG:      Round(netTransfer, 3),
			TransferCount:      transferCount,
			UnexplainedKG:      unexplained,
			UncertaintyKG:      propagated.CombinedKG,
			ExceedsUncertainty: math.Abs(unexplained) > propagated.CombinedKG,
		})
	}
	unallocated := make([]UnallocatedTransfer, 0)
	for index, transfer := range transfers {
		if attributed[index] {
			continue
		}
		unallocated = append(unallocated, UnallocatedTransfer{
			TransferID: transfer.TransferID,
			Reason:     "转移时间段跨越快照边界或位于快照链之外，不能唯一归入某一段，未计入分段核对",
		})
	}
	return segments, unallocated, nil
}
