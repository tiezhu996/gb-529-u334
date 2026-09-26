package balance

import (
	"fmt"
	"math"
	"sort"
	"time"

	"lng-boiloff-gas-balance/backend/internal/constants"
)

type UncertaintyInput struct {
	Source         string
	EntityID       uint
	MassKG         float64
	UncertaintyPct float64
}

type UncertaintyResult struct {
	CombinedKG float64
	Components []float64
}

func PropagateUncertainty(inputs []UncertaintyInput) (UncertaintyResult, error) {
	if len(inputs) < 2 {
		return UncertaintyResult{}, fmt.Errorf("at least opening and closing uncertainty inputs are required")
	}
	sumSquares := 0.0
	components := make([]float64, 0, len(inputs))
	for index, input := range inputs {
		if input.MassKG < 0 || !finite(input.MassKG) {
			return UncertaintyResult{}, fmt.Errorf("uncertainty input %d mass is invalid", index)
		}
		if err := ValidateUncertainty(input.UncertaintyPct); err != nil {
			return UncertaintyResult{}, fmt.Errorf("uncertainty input %d: %w", index, err)
		}
		absolute := input.MassKG * PercentFraction(input.UncertaintyPct)
		components = append(components, Round(absolute, 3))
		sumSquares += absolute * absolute
	}
	return UncertaintyResult{CombinedKG: Round(math.Sqrt(sumSquares), 3), Components: components}, nil
}

func ClassifyDeviation(deviation, uncertainty float64, valid bool) constants.DeviationLevel {
	if !valid || uncertainty <= 0 || !finite(deviation) || !finite(uncertainty) {
		return constants.DeviationInvalid
	}
	ratio := math.Abs(deviation) / uncertainty
	switch {
	case ratio <= 1:
		return constants.DeviationWithinUncertainty
	case ratio <= 2:
		return constants.DeviationWatch
	default:
		return constants.DeviationInvestigate
	}
}

func ConfidenceInterval(deviation, uncertainty float64) (float64, float64) {
	return Round(deviation-uncertainty, 3), Round(deviation+uncertainty, 3)
}

// SegmentSnapshotInput 是参与期内分段复核的一条有效计量快照。
type SegmentSnapshotInput struct {
	ID             uint
	MeasuredAt     time.Time
	MassKG         float64
	UncertaintyPct float64
}

// SegmentTransferInput 是平衡期间内一条已确认物理转移。
type SegmentTransferInput struct {
	ID             uint
	OperationType  string
	StartAt        time.Time
	MassKG         float64
	UncertaintyPct float64
}

// SegmentReconciliation 记录相邻两条有效快照之间的一次质量核对结果。
type SegmentReconciliation struct {
	Sequence          int
	OpeningSnapshotID uint
	ClosingSnapshotID uint
	StartAt           time.Time
	EndAt             time.Time
	OpeningMassKG     float64
	ClosingMassKG     float64
	NetTransferKG     float64
	TransferIDs       []uint
	DiscrepancyKG     float64
	UncertaintyKG     float64
	Unexplained       bool
	Level             constants.DeviationLevel
}

// SegmentReconciliationResult 汇总全部分段核对结果，以及未落入任何快照区间的转移。
type SegmentReconciliationResult struct {
	Segments                []SegmentReconciliation
	OutsideChainTransferIDs []uint
}

// ReconcileSegments 将期内有效快照按时间排序，逐段核对相邻快照质量变化与该段
// 确认转移净量。确认转移按开始时刻归属到所处快照区间；区间差值 = 区间期初质量 +
// 区间确认转移净量 - 区间期末质量，差值绝对值大于该段合成不确定度时记为未解释区间。
func ReconcileSegments(snapshots []SegmentSnapshotInput, transfers []SegmentTransferInput) (SegmentReconciliationResult, error) {
	if len(snapshots) < 2 {
		return SegmentReconciliationResult{}, fmt.Errorf("at least two valid snapshots are required for interval reconciliation")
	}
	ordered := make([]SegmentSnapshotInput, len(snapshots))
	copy(ordered, snapshots)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].MeasuredAt.Equal(ordered[j].MeasuredAt) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].MeasuredAt.Before(ordered[j].MeasuredAt)
	})
	segments := make([]SegmentReconciliation, 0, len(ordered)-1)
	uncertaintyInputs := make([][]UncertaintyInput, len(ordered)-1)
	inflows := make([][]float64, len(ordered)-1)
	outflows := make([][]float64, len(ordered)-1)
	for index := 0; index+1 < len(ordered); index++ {
		opening, closing := ordered[index], ordered[index+1]
		if !closing.MeasuredAt.After(opening.MeasuredAt) {
			return SegmentReconciliationResult{}, fmt.Errorf("snapshots %d and %d do not form an increasing time interval", opening.ID, closing.ID)
		}
		segments = append(segments, SegmentReconciliation{
			Sequence:          index + 1,
			OpeningSnapshotID: opening.ID,
			ClosingSnapshotID: closing.ID,
			StartAt:           opening.MeasuredAt,
			EndAt:             closing.MeasuredAt,
			OpeningMassKG:     opening.MassKG,
			ClosingMassKG:     closing.MassKG,
			TransferIDs:       []uint{},
		})
		uncertaintyInputs[index] = []UncertaintyInput{
			{Source: "segment_opening_snapshot", EntityID: opening.ID, MassKG: opening.MassKG, UncertaintyPct: opening.UncertaintyPct},
			{Source: "segment_closing_snapshot", EntityID: closing.ID, MassKG: closing.MassKG, UncertaintyPct: closing.UncertaintyPct},
		}
	}
	outside := []uint{}
	for _, transfer := range transfers {
		target := -1
		for index := range segments {
			if !transfer.StartAt.Before(segments[index].StartAt) && transfer.StartAt.Before(segments[index].EndAt) {
				target = index
				break
			}
		}
		if target < 0 {
			outside = append(outside, transfer.ID)
			continue
		}
		segments[target].TransferIDs = append(segments[target].TransferIDs, transfer.ID)
		if transfer.OperationType == "inflow" {
			inflows[target] = append(inflows[target], transfer.MassKG)
		} else {
			outflows[target] = append(outflows[target], transfer.MassKG)
		}
		uncertaintyInputs[target] = append(uncertaintyInputs[target], UncertaintyInput{
			Source: "transfer_" + transfer.OperationType, EntityID: transfer.ID, MassKG: transfer.MassKG, UncertaintyPct: transfer.UncertaintyPct,
		})
	}
	for index := range segments {
		net, err := NetTransfer(inflows[index], outflows[index])
		if err != nil {
			return SegmentReconciliationResult{}, fmt.Errorf("segment %d net transfer: %w", segments[index].Sequence, err)
		}
		discrepancy, err := PhysicalBalance(segments[index].OpeningMassKG, net, segments[index].ClosingMassKG)
		if err != nil {
			return SegmentReconciliationResult{}, fmt.Errorf("segment %d physical balance: %w", segments[index].Sequence, err)
		}
		propagated, err := PropagateUncertainty(uncertaintyInputs[index])
		if err != nil {
			return SegmentReconciliationResult{}, fmt.Errorf("segment %d uncertainty: %w", segments[index].Sequence, err)
		}
		segments[index].NetTransferKG = net
		segments[index].DiscrepancyKG = discrepancy
		segments[index].UncertaintyKG = propagated.CombinedKG
		segments[index].Level = ClassifyDeviation(discrepancy, propagated.CombinedKG, true)
		segments[index].Unexplained = math.Abs(discrepancy) > propagated.CombinedKG
	}
	return SegmentReconciliationResult{Segments: segments, OutsideChainTransferIDs: outside}, nil
}
