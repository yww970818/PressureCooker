package reporting

import (
	"fmt"
	"math"
)

func ValidateTargets(t Targets) error {
	for k, v := range map[string]*float64{"min_rps": t.MinRPS, "min_success_rate": t.MinSuccessRate, "max_p95_ms": t.MaxP95MS, "max_p99_ms": t.MaxP99MS, "min_duration_seconds": t.MinDurationSeconds} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 1e15 || (k == "min_success_rate" && *v > 100)) {
			return fmt.Errorf("invalid target %s", k)
		}
	}
	return nil
}
func Evaluate(m Metrics, q Quality, t Targets, run string) Verdict {
	v := Verdict{Status: "NOT_SET", Label: "未设置标准", Checks: []Check{}}
	if t == (Targets{}) {
		return v
	}
	v.Status = "PASS"
	v.Label = "达标"
	incomplete := run != "COMPLETED" || q.MetricStatus != "COMPLETE" || !q.ScopeConfirmed || q.NodeCoverage != "COMPLETE" || m.SampleCount == 0 || ValidateTargets(t) != nil
	add := func(k string, target, actual *float64, min bool, ex *thresholdCount, pct int64) {
		if target == nil {
			return
		}
		unit := "ms"
		if k == "min_rps" {
			unit = "requests/s"
		} else if k == "min_success_rate" {
			unit = "%"
		} else if k == "min_duration_seconds" {
			unit = "s"
		}
		c := Check{Unit: unit, Key: k, Label: k, Target: *target, Actual: actual, Status: "PASS"}
		if actual == nil {
			c.Status = "INCOMPLETE"
			incomplete = true
		} else {
			pass := *actual <= *target
			if min {
				pass = *actual >= *target
			}
			if ex != nil && ex.target == *target {
				pass = ex.atOrBelow >= (ex.total*pct+99)/100
				c.Detail = "判定使用原始样本阈值计数"
			} else if pct > 0 {
				c.Status = "INCOMPLETE"
				incomplete = true
			}
			if !pass && c.Status != "INCOMPLETE" {
				c.Status = "FAIL"
				v.Status = "FAIL"
			}
		}
		v.Checks = append(v.Checks, c)
	}
	add("min_rps", t.MinRPS, m.RPS, true, nil, 0)
	add("min_success_rate", t.MinSuccessRate, m.SuccessRate, true, nil, 0)
	add("max_p95_ms", t.MaxP95MS, m.P95MS, false, q.exactP95, 95)
	add("max_p99_ms", t.MaxP99MS, m.P99MS, false, q.exactP99, 99)
	add("min_duration_seconds", t.MinDurationSeconds, m.DurationSeconds, true, nil, 0)
	if incomplete {
		v.Status = "INCOMPLETE"
		v.Label = "数据不完整"
		v.Explanation = "任务、样本分类、有效性、节点覆盖或精确阈值证据不足"
	} else if v.Status == "FAIL" {
		v.Label = "未达标"
	}
	return v
}
