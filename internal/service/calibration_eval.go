package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"seiswatch/internal/model"
)

// Calibration acceptance thresholds.
const (
	massCenterMaxAbsOffset = 500.0 // counts
	massCenterMaxVariance  = 1e4   // counts^2
	gainCheckMinRatio      = 0.95  // measured/nominal gain ratio
	gainCheckMaxRatio      = 1.05  //
	gainCheckMinSNR        = 20.0  // dB
)

// EvaluateCalibMetrics judges raw calibration measurements against the
// acceptance criteria for the job kind. It returns pass/fail plus the list
// of failing factors ("missing metric X", "offset 512 > 500", ...).
// An unknown kind yields an error; missing metrics count as failures so a
// partially reported run can never pass silently.
func EvaluateCalibMetrics(kind model.CalibrationKind, metrics map[string]float64) (bool, []string, error) {
	switch kind {
	case model.CalibMassCenter:
		pass, factors := evaluateMassCenter(metrics)
		return pass, factors, nil
	case model.CalibGainCheck:
		pass, factors := evaluateGainCheck(metrics)
		return pass, factors, nil
	default:
		return false, nil, fmt.Errorf("unknown calibration kind %q", kind)
	}
}

func evaluateMassCenter(m map[string]float64) (bool, []string) {
	var factors []string
	offset, ok := m["offset"]
	if !ok {
		factors = append(factors, "missing metric offset")
	} else if math.Abs(offset) > massCenterMaxAbsOffset {
		factors = append(factors, fmt.Sprintf("offset %.1f exceeds ±%.0f counts", offset, massCenterMaxAbsOffset))
	}
	variance, ok := m["variance"]
	if !ok {
		factors = append(factors, "missing metric variance")
	} else if variance >= massCenterMaxVariance {
		factors = append(factors, fmt.Sprintf("variance %.4g exceeds %.4g", variance, massCenterMaxVariance))
	}
	return len(factors) == 0, factors
}

func evaluateGainCheck(m map[string]float64) (bool, []string) {
	var factors []string
	ratio, ok := m["gain_ratio"]
	if !ok {
		factors = append(factors, "missing metric gain_ratio")
	} else if ratio < gainCheckMinRatio || ratio > gainCheckMaxRatio {
		factors = append(factors, fmt.Sprintf("gain_ratio %.4f outside [%.2f, %.2f]", ratio, gainCheckMinRatio, gainCheckMaxRatio))
	}
	snr, ok := m["snr"]
	if !ok {
		factors = append(factors, "missing metric snr")
	} else if snr < gainCheckMinSNR || math.IsNaN(snr) {
		factors = append(factors, fmt.Sprintf("snr %.2f dB below minimum %.0f dB", snr, gainCheckMinSNR))
	}
	return len(factors) == 0, factors
}

// CompleteEvaluated evaluates the submitted metrics first, then finishes
// the running job as succeeded or failed accordingly. The failing factor
// list is stored back into result_metrics under synthetic keys so auditors
// see why a job failed without leaving the database.
func (s *CalibrationService) CompleteEvaluated(id int64, metrics map[string]float64) (*model.CalibrationJob, error) {
	job, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if job.State != model.CalibRunning {
		return nil, fmt.Errorf("job %d in state %s cannot complete: %w", id, job.State, ErrInvalidState)
	}
	pass, factors, err := EvaluateCalibMetrics(job.Kind, metrics)
	if err != nil {
		return nil, fmt.Errorf("evaluate job %d (%s): %w", id, job.Kind, err)
	}

	finalMetrics := cloneMetrics(metrics)
	if !pass {
		for i, f := range factors {
			finalMetrics[fmt.Sprintf("fail_factor_%02d", i+1)] = float64(fnv32(f))
		}
	}

	state := model.CalibSucceeded
	if !pass {
		state = model.CalibFailed
	}
	if err := s.db.Calibrations.FinishWithResult(context.Background(), id, state, finalMetrics, timeNow()); err != nil {
		return nil, err
	}
	return s.get(id)
}

// cloneMetrics copies the map so callers cannot mutate stored results via
// their original slice/map reference.
func cloneMetrics(m map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(m)+2)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// fnv32 hashes a failure reason to a stable numeric fingerprint because
// result_metrics only accepts float64 values.
func fnv32(s string) uint32 {
	const (
		offset32 = 2166136261
		prime32  = 16777619
	)
	h := uint32(offset32)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= prime32
	}
	return h
}

// RequiredMetrics lists the metric keys each calibration kind must report.
// Handlers use it to validate submission forms before a run finishes.
func RequiredMetrics(kind model.CalibrationKind) ([]string, error) {
	switch kind {
	case model.CalibMassCenter:
		return []string{"offset", "variance"}, nil
	case model.CalibGainCheck:
		return []string{"gain_ratio", "snr"}, nil
	default:
		return nil, fmt.Errorf("unknown calibration kind %q", kind)
	}
}

// MissingRequiredMetrics reports which mandatory keys are absent from the
// submitted metric set; an unknown kind yields an error.
func MissingRequiredMetrics(kind model.CalibrationKind, metrics map[string]float64) ([]string, error) {
	required, err := RequiredMetrics(kind)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, key := range required {
		if _, ok := metrics[key]; !ok {
			missing = append(missing, key)
		}
	}
	return missing, nil
}

// SummarizeCalibration renders a one-line verdict for logs and dashboards,
// e.g. "job 7 (gain-check) succeeded in 4m12s".
func SummarizeCalibration(job *model.CalibrationJob) string {
	if job == nil {
		return "job <nil>"
	}
	verdict := string(job.State)
	if elapsed := job.Elapsed(); elapsed > 0 {
		verdict += fmt.Sprintf(" in %s", elapsed.Round(time.Second))
	}
	return fmt.Sprintf("job %d (%s) %s", job.ID, job.Kind, verdict)
}
