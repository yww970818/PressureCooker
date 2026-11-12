package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"pressurecooker/manager/reporting"
	"sort"
	"strconv"
	"strings"
	"time"
)

const pressureMaxSourceBytes int64 = 2 << 30

type pressureClaim struct {
	ID, ProjectID, RunID, Token string
	Attempts                    int
}

func (p *Platform) claimPressure() (claim pressureClaim, ok bool, err error) {
	err = p.transaction(func(tx *Tx) error {
		ok = false
		for {
			var id, project, run string
			var attempts int
			e := tx.QueryRow("SELECT id,project_id,run_id,attempts FROM pressure_report_jobs WHERE ((status IN ('QUEUED','RETRY') AND next_attempt_at<=?) OR (status='PROCESSING' AND lease_until<?)) ORDER BY created_at,id LIMIT 1 FOR UPDATE", epoch(), epoch()).Scan(&id, &project, &run, &attempts)
			if errors.Is(e, sql.ErrNoRows) {
				return nil
			}
			if e != nil {
				return e
			}
			if attempts >= 3 {
				if e = setPressureFailure(tx, id, "FAILED", "报告处理连续失去租约或已用尽重试次数", 0); e != nil {
					return e
				}
				continue
			}
			claim = pressureClaim{id, project, run, newID("lease-"), attempts + 1}
			if _, e = tx.Exec("UPDATE pressure_report_jobs SET status='PROCESSING',attempts=?,lease_token=?,lease_until=? WHERE id=?", claim.Attempts, claim.Token, epoch()+90, id); e != nil {
				return e
			}
			report, e := get(tx, "pressure_reports", id)
			if e != nil {
				return e
			}
			report["status"] = "PROCESSING"
			report["error"] = ""
			_, e = tx.Exec("UPDATE pressure_reports SET status='PROCESSING',data=? WHERE id=?", string(encoded(report)), id)
			ok = e == nil
			return e
		}
	})
	return
}
func (p *Platform) pressureLoop(ctx context.Context) {
	for ctx.Err() == nil {
		claim, ok, e := p.claimPressure()
		if e != nil || !ok {
			if !pause(ctx, 2*time.Second) {
				return
			}
			continue
		}
		p.workPressure(ctx, claim)
	}
}
func (p *Platform) workPressure(parent context.Context, c pressureClaim) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				res, e := p.DB.Exec("UPDATE pressure_report_jobs SET lease_until=? WHERE id=? AND lease_token=? AND status='PROCESSING' AND lease_until>?", epoch()+90, c.ID, c.Token, epoch())
				if e != nil {
					cancel()
					return
				}
				n, _ := res.RowsAffected()
				if n != 1 {
					cancel()
					return
				}
			}
		}
	}()
	scoped := p.forProject(c.ProjectID)
	report, e := scoped.pressureGet(c.ID)
	if e != nil {
		p.failPressure(c, "报告记录不可用")
		return
	}
	run, e := scoped.get("runs", c.RunID)
	if e != nil {
		p.failPressure(c, "任务快照不可用")
		return
	}
	if str(report["source_fingerprint"]) != pressureSourceIdentity(run) {
		p.failPressure(c, "原始来源已变化，请重新生成报告版本")
		return
	}
	targets, e := pressureTargets(report["targets"])
	if e != nil {
		p.failPressure(c, "验收目标无效")
		return
	}
	options := reporting.ParseOptions{AdaptiveDashboard: true, Targets: targets, RunStatus: str(run["status"]), ExpectedNodes: []string{}, Redactor: pressureRedactor(run), TimestampIsEnd: obj(run["properties"])["sampleresult.timestamp.start"] == "false", ScopeConfirmed: p.pressureOrdinaryScope(run)}
	analysisOptions, e := pressureAnalysisOptions(report["analysis_options"])
	if e != nil {
		p.failPressure(c, "报告分析设置无效")
		return
	}
	options.AnalyticsOptions = analysisOptions
	for _, v := range arr(run["workers"]) {
		options.ExpectedNodes = append(options.ExpectedNodes, str(v))
	}
	safeNodes := make([]string, len(options.ExpectedNodes))
	for i, n := range options.ExpectedNodes {
		safeNodes[i] = reporting.PublicNodeID(n, options.Redactor)
	}
	analysis := reporting.Analysis{Quality: reporting.Quality{MetricStatus: "MISSING", LogStatus: "MISSING", ExpectedNodes: safeNodes, ObservedNodes: []string{}, NodeCoverage: "UNKNOWN", Warnings: []string{"缺少有效的原始采样文件"}}, Timeline: []reporting.Point{}, Steps: []reporting.Step{}, Issues: []reporting.Issue{}, Findings: []reporting.Finding{}}
	analysis.Dashboard = reporting.Dashboard{Version: 1, Options: analysisOptions, Capabilities: reporting.DashboardCapabilities{ActiveThreads: "MISSING", Latency: "MISSING", Connect: "MISSING", ResponseCodes: "MISSING", Bytes: "MISSING", Transactions: "MISSING"}, Warnings: []string{"缺少有效的原始采样文件"}, Scopes: []reporting.DashboardScope{}}
	sources := []any{}
	logCount, logVerified, logEmpty := 0, 0, 0
	for _, name := range rawFiles {
		src := Obj{"kind": "jmeter_log", "name": name, "status": "MISSING", "error": "回调中未提供此文件", "valid_rows": 0, "invalid_rows": 0}
		if name == "results.jtl" {
			src["kind"] = "jtl"
		}
		var file Obj
		for _, v := range arr(obj(run["raw_archive"])["files"]) {
			if str(obj(v)["name"]) == name {
				file = obj(v)
				break
			}
		}
		if len(file) == 0 {
			sources = append(sources, src)
			continue
		}
		if str(file["status"]) != "ARCHIVED" {
			src["error"] = "该文件尚未归档"
			sources = append(sources, src)
			continue
		}
		ref := objectRef(file)
		if str(run["execution_mode"]) != "redis" && ref.Key == "" {
			ref.Key = str(file["object_key"])
			ref.Bucket = p.Config.ArchiveBucket
		}
		prefix, pe := rawPrefix(run)
		if pe != nil || ref.Bucket != p.Config.ArchiveBucket || ref.Key != prefix+name || !objectSHA.MatchString(ref.SHA256) || ref.Size < 0 || ref.Size > pressureMaxSourceBytes {
			p.failPressure(c, "原始文件引用不合法或超过2GiB单文件限制")
			return
		}
		src["bucket"] = ref.Bucket
		src["key"] = ref.Key
		src["sha256"] = ref.SHA256
		src["size"] = ref.Size
		src["download_url"] = "/api/runs/" + c.RunID + "/artifacts/" + name
		store, se := p.objectStore()
		if se != nil {
			p.failPressure(c, "原始文件存储暂不可用")
			return
		}
		reader, se := store.Get(ctx, ref)
		if se != nil {
			p.failPressure(c, "原始文件下载或校验失败；请检查对象是否仍存在")
			return
		}
		tmp, se := pressureSpool(p.Config.StateDir)
		if se != nil {
			reader.Close()
			p.failPressure(c, "临时文件不可用")
			return
		}
		// Get verifies actual size and SHA on EOF. Never publish metrics from an unverified prefix.
		n, se := io.Copy(tmp, io.LimitReader(reader, pressureMaxSourceBytes+1))
		reader.Close()
		if se != nil || n != ref.Size {
			tmp.Close()
			p.failPressure(c, "原始文件下载或SHA256校验失败")
			return
		}
		src["download_status"] = "VERIFIED"
		_, se = tmp.Seek(0, io.SeekStart)
		if se == nil && name == "results.jtl" {
			analysis, se = reporting.ParseJTL(ctx, tmp, options)
			if se == nil {
				src["valid_rows"] = analysis.Metrics.SampleCount + analysis.Metrics.TransactionCount
				src["invalid_rows"] = analysis.Quality.InvalidRows
			}
		}
		if se == nil && name != "results.jtl" {
			var issues []reporting.Issue
			var lines int64
			logVerified++
			issues, lines, se = reporting.AnalyzeLogs(ctx, tmp, name, options.Redactor)
			analysis.Issues = append(analysis.Issues, issues...)
			src["valid_rows"] = lines
			if se == nil && lines == 0 {
				src["status"] = "EMPTY"
				src["error"] = "日志没有非空内容"
				logEmpty++
			} else if se == nil {
				logCount++
			} else {
				src["status"] = "PARSE_PARTIAL"
				if lines == 0 {
					src["status"] = "PARSE_FAILED"
				}
				src["error"] = "日志解析超出大小/事件限制或含坏行，仅保留已解析证据"
				se = nil
			}
		}
		tmp.Close()
		if se != nil {
			p.failPressure(c, pressureJTLFailure(se))
			return
		}
		if src["status"] == "MISSING" {
			src["status"] = "VERIFIED"
			src["error"] = ""
		}
		sources = append(sources, src)
	}
	threadSources, threadErr := p.pressureLoadThreadObservations(ctx, run, &analysis)
	if threadErr != nil {
		p.failPressure(c, "独立线程观测来源读取失败，将自动重试")
		return
	}
	sources = append(sources, threadSources...)
	pressureNodeLogIssues(&analysis, run)
	switch logCount {
	case 0:
		analysis.Quality.LogStatus = "MISSING"
		if logVerified > 0 {
			analysis.Quality.LogStatus = "PARTIAL"
		}
		if logEmpty == 2 {
			analysis.Quality.LogStatus = "EMPTY"
		}
	case 2:
		analysis.Quality.LogStatus = "COMPLETE"
	default:
		analysis.Quality.LogStatus = "PARTIAL"
	}
	if logCount < 2 {
		analysis.Quality.Warnings = append(analysis.Quality.Warnings, "压测端日志不完整，不能据此判断没有异常")
	}
	analysis.Verdict = reporting.Evaluate(analysis.Metrics, analysis.Quality, targets, str(run["status"]))

	if e = p.publishPressure(c, report, run, analysis, sources); e != nil {
		p.failPressure(c, "报告入库失败，将自动重试")
	}
}

// Parser errors are deliberately mapped to controlled text: file contents and
// storage errors can contain credentials and must never become report messages.
func pressureJTLFailure(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "JTL解析已取消或超时"
	}
	switch err.Error() {
	case "JTL dashboard point limit exceeded", "JTL timeline bucket limit exceeded", "JTL dashboard histogram cell limit exceeded":
		return "JTL解析失败：趋势粒度已按数据量自动增大，仍超过安全聚合边界"
	case "JTL node identity collision after redaction":
		return "JTL解析失败：执行节点身份冲突，无法安全归属采样"
	default:
		return "JTL解析失败：格式无效或超过文件、采样、步骤、节点或错误分类边界"
	}
}

// Unlink before downloading. A process exit closes the last descriptor, even on SIGKILL.
func pressureSpool(dir string) (*os.File, error) {
	f, err := os.CreateTemp(dir, "pressure-source-*")
	if err != nil {
		return nil, err
	}
	if err = os.Remove(f.Name()); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
func (p *Platform) pressureOrdinaryScope(run Obj) bool {
	hash := str(run["plan_sha256"])
	if !objectSHA.MatchString(hash) {
		return false
	}
	path, e := safePath(p.Config.PlansDir, "snapshots/"+hash+".jmx", true)
	if e != nil {
		return false
	}
	content, e := os.ReadFile(path)
	if e != nil || digest(content) != hash {
		return false
	}
	// Any custom controller with transaction aggregation remains unconfirmed. The parser
	// may separately confirm typed sample-kind columns; arbitrary labels are not evidence.
	lower := strings.ToLower(string(content))
	return !strings.Contains(lower, "transactioncontroller") && !strings.Contains(lower, "transaction_sampler")
}
func (p *Platform) failPressure(c pressureClaim, message string) {
	_ = p.transaction(func(tx *Tx) error {
		var token string
		var until float64
		if e := tx.QueryRow("SELECT lease_token,lease_until FROM pressure_report_jobs WHERE id=? FOR UPDATE", c.ID).Scan(&token, &until); e != nil {
			return e
		}
		if token != c.Token || until < epoch() {
			return nil
		}
		status := "RETRY"
		if c.Attempts >= 3 {
			status = "FAILED"
		}
		return setPressureFailure(tx, c.ID, status, message, epoch()+float64(5*c.Attempts))
	})
}
func setPressureFailure(tx *Tx, id, status, message string, next float64) error {
	report, e := get(tx, "pressure_reports", id)
	if e != nil {
		return e
	}
	report["status"] = status
	report["error"] = message
	if status == "FAILED" {
		report["finished_at"] = now()
	}
	if _, e = tx.Exec("UPDATE pressure_reports SET status=?,finished_at=?,data=? WHERE id=?", status, str(report["finished_at"]), string(encoded(report)), id); e != nil {
		return e
	}
	_, e = tx.Exec("UPDATE pressure_report_jobs SET status=?,next_attempt_at=?,lease_until=0,lease_token='',last_error=? WHERE id=?", status, next, message, id)
	return e
}
func (p *Platform) publishPressure(c pressureClaim, report, run Obj, a reporting.Analysis, sources []any) error {
	if len(encoded(a.Dashboard)) > 16<<20 {
		return errors.New("高级分析超过16MiB边界，请增大报告时间粒度")
	}
	return p.transaction(func(tx *Tx) error {
		var token, status string
		var until float64
		if e := tx.QueryRow("SELECT lease_token,status,lease_until FROM pressure_report_jobs WHERE id=? FOR UPDATE", c.ID).Scan(&token, &status, &until); e != nil {
			return e
		}
		if token != c.Token || status != "PROCESSING" || until < epoch() {
			return errors.New("stale report lease")
		}
		sort.SliceStable(a.Steps, func(i, j int) bool {
			if a.Steps[i].P95MS == nil {
				return false
			}
			if a.Steps[j].P95MS == nil {
				return true
			}
			return *a.Steps[i].P95MS > *a.Steps[j].P95MS
		})
		a.Issues = pressureClusterIssues(a.Issues)
		sets := map[string][]any{"sources": sources, "steps": {}, "timeline": {}, "issues": {}, "metrics": {}, "findings": {}}
		for _, v := range a.Steps {
			sets["steps"] = append(sets["steps"], dtoObject(v))
		}
		globalStepCount := len(sets["steps"])
		for _, v := range a.NodeSteps {
			sets["steps"] = append(sets["steps"], dtoObject(v))
		}
		for _, v := range a.Timeline {
			sets["timeline"] = append(sets["timeline"], dtoObject(v))
		}
		for _, v := range a.Issues {
			sets["issues"] = append(sets["issues"], dtoObject(v))
		}
		sourceRefs, logRefs, issueRefs := []string{}, []string{}, []string{}
		for i, v := range sources {
			id := pressureEvidenceID(c.ID, "sources", i)
			obj(v)["id"] = id
			if obj(v)["name"] == "results.jtl" {
				sourceRefs = append(sourceRefs, id)
			} else if obj(v)["kind"] == "jmeter_log" && obj(v)["status"] != "VERIFIED" {
				logRefs = append(logRefs, id)
			}
		}
		for i, v := range a.Issues {
			if v.Category == "sample_failure" && len(issueRefs) < 10 {
				issueRefs = append(issueRefs, pressureEvidenceID(c.ID, "issues", i))
			}
		}
		if len(issueRefs) == 0 {
			issueRefs = sourceRefs
		}
		for _, v := range pressureFindings(a, c.ID, sourceRefs, logRefs, issueRefs) {
			sets["findings"] = append(sets["findings"], dtoObject(v))
		}
		for k, v := range dtoObject(a.Metrics) {
			unit := pressureUnits[k]
			if _, ok := v.(float64); !ok || unit == "" {
				continue
			}
			sets["metrics"] = append(sets["metrics"], Obj{"key": k, "value": v, "unit": unit, "source_kind": "jtl", "schema_version": reporting.SchemaVersion, "origin": "jtl", "source_fingerprint": report["source_fingerprint"]})
		}
		for kind, items := range sets {
			table := "pressure_report_" + kind
			if _, e := tx.Exec("DELETE FROM "+table+" WHERE report_id=?", c.ID); e != nil {
				return e
			}
			for i, value := range items {
				m := obj(value)
				id := pressureEvidenceID(c.ID, kind, i)
				m["id"] = id
				label := str(m["label"])
				if len([]rune(label)) > 500 {
					label = string([]rune(label)[:500])
				}
				count := number(m["count"])
				if kind == "steps" {
					count = number(m["failure_count"])
				}
				var numeric any
				if v := m["p95_ms"]; v != nil {
					numeric = number(v)
				}
				columns := []string{"id", "project_id", "report_id", "created_at", "position", "kind", "label", "count_value", "number_value", "data"}
				if kind == "metrics" {
					label = str(m["key"])
					if m["value"] != nil {
						numeric = number(m["value"])
					}
					m["metric_key"] = m["key"]
				}
				values := []any{id, c.ProjectID, c.ID, now(), i, kind, label, count, numeric, string(encoded(m))}
				if kind == "sources" {
					m["object_key"] = m["key"]
				}
				for _, column := range pressureExtraColumns[table] {
					columns = append(columns, column.Name)
					value := m[column.Name]
					if value == nil && strings.HasSuffix(column.Type, " NOT NULL") {
						if strings.Contains(column.Type, "INT") {
							value = 0
						} else {
							value = ""
						}
					}
					values = append(values, value)
				}
				query := "INSERT INTO " + table + "(`" + strings.Join(columns, "`,`") + "`) VALUES(" + strings.TrimSuffix(strings.Repeat("?,", len(values)), ",") + ")"
				if _, e := tx.Exec(query, values...); e != nil {
					return e
				}
			}
		}
		report["status"] = "READY"
		report["finished_at"] = now()
		report["error"] = ""
		report["metrics"] = a.Metrics
		report["quality"] = a.Quality
		report["verdict"] = a.Verdict
		report["verdict_status"] = a.Verdict.Status
		report["sources"] = sets["sources"]
		report["timeline"] = sets["timeline"]
		report["steps_preview"] = sets["steps"][:min(10, globalStepCount)]
		report["node_detail_version"] = 1
		report["analytics_version"] = a.Dashboard.Version
		dashboard, e := p.storeDashboard(tx, c.ID, c.ProjectID, a.Dashboard)
		if e != nil {
			return e
		}
		report["dashboard"] = dashboard
		requestCaptured := pressureMergeRequestSamples(&a, run)
		report["request_samples"] = []any{}
		for i, sample := range a.RequestSamples {
			sample.ID = pressureEvidenceID(c.ID, "samples", i)
			report["request_samples"] = append(arr(report["request_samples"]), dtoObject(sample))
		}
		report["request_samples_version"] = 0
		if requestCaptured {
			report["request_samples_version"] = 1
		}
		report["request_samples_truncated"] = a.RequestSamplesTruncated
		report["analysis_options"] = a.Dashboard.Options
		report["nodes"], report["node_logs"] = pressureNodeDetailData(c.ID, a, run)
		report["issues_preview"] = sets["issues"][:min(10, len(sets["issues"]))]
		report["findings"] = sets["findings"]
		report["extensions"] = Obj{"schema_version": "1", "jvm": Obj{"status": "NOT_CONNECTED", "label": "未接入"}, "middleware": Obj{"status": "NOT_CONNECTED", "label": "未接入"}, "k8s": Obj{"status": "NOT_CONNECTED", "label": "未接入"}, "ai": Obj{"status": "NOT_ANALYZED", "label": "未分析"}}
		m := a.Metrics
		if _, e := tx.Exec("UPDATE pressure_reports SET status='READY',finished_at=?,verdict_status=?,sample_count=?,failure_count=?,success_rate=?,rps=?,p95_ms=?,p99_ms=?,duration_seconds=?,success_count=?,mean_ms=?,min_ms=?,max_ms=?,p50_ms=?,p90_ms=?,bytes_received=?,bytes_sent=?,transaction_count=?,sample_scope=?,data=? WHERE id=?", report["finished_at"], a.Verdict.Status, m.SampleCount, m.FailureCount, m.SuccessRate, m.RPS, m.P95MS, m.P99MS, m.DurationSeconds, m.SuccessCount, m.MeanMS, m.MinMS, m.MaxMS, m.P50MS, m.P90MS, m.BytesReceived, m.BytesSent, m.TransactionCount, m.SampleScope, string(encoded(report)), c.ID); e != nil {
			return e
		}
		_, e = tx.Exec("UPDATE pressure_report_jobs SET status='READY',lease_token='',lease_until=0,last_error='' WHERE id=?", c.ID)
		return e
	})
}

// These findings describe measured evidence, never infer a SUT root cause from load-generator logs.
var pressureUnits = map[string]string{"sample_count": "count", "success_count": "count", "failure_count": "count", "transaction_count": "count", "bytes_received": "bytes", "bytes_sent": "bytes", "duration_seconds": "s", "rps": "samples/s", "success_rate": "%", "mean_ms": "ms", "min_ms": "ms", "max_ms": "ms", "p50_ms": "ms", "p90_ms": "ms", "p95_ms": "ms", "p99_ms": "ms"}

func pressureEvidenceID(report, kind string, i int) string {
	return "evidence-" + digest([]byte(report + "/" + kind + "/" + strconv.Itoa(i)))[:32]
}
func pressureFindings(a reporting.Analysis, reportID string, sources, logs, issues []string) []reporting.Finding {
	out := []reporting.Finding{}
	add := func(level, title, detail, recommendation string, refs []string) {
		out = append(out, reporting.Finding{Origin: "rule", Level: level, Title: title, Detail: detail, Recommendation: recommendation, EvidenceRefs: refs})
	}
	if a.Verdict.Status == "NOT_SET" {
		add("INFO", "未设置验收标准", "报告展示本次实际表现，不能据此认定能够支撑活动峰值。", "压测前为任务设置验收目标；历史结果可新增测后评审版本。", []string{reportID})
	}
	if a.Metrics.FailureCount > 0 {
		add("WARN", fmt.Sprintf("发现%d条失败采样", a.Metrics.FailureCount), "失败按脚本保存的采样结果及断言识别，不等同于业务工单或用户数量。", "在失败原因中核对响应摘要和原始文件，区分断言失败与网络错误。", issues)
	}
	if a.Quality.NodeCoverage != "COMPLETE" {
		add("WARN", "执行节点覆盖证据不足", "采样中的节点标识未能完整对应本任务预期节点，整体不判定为达标。", "核对执行节点和原始文件中的engine_id。", sources)
	}
	if a.Quality.LogStatus != "COMPLETE" {
		add("WARN", "压测端日志不完整", "缺失日志不能作为没有异常的证据。JMeter日志也不代表被测服务日志。", "补齐归档；服务端、JVM和中间件证据后续单独接入。", logs)
	}
	return out
}
func pressureClusterIssues(items []reporting.Issue) []reporting.Issue {
	out := []reporting.Issue{}
	index := map[string]int{}
	for _, v := range items {
		key := v.NodeID + "\x00" + v.StepKey + "\x00" + v.SourceKind + "\x00" + v.SourceName + "\x00" + v.Category + "\x00" + v.StepLabel + "\x00" + v.Message
		if i, ok := index[key]; ok {
			q := &out[i]
			q.Count += v.Count
			if v.FirstAt != nil && (q.FirstAt == nil || *v.FirstAt < *q.FirstAt) {
				q.FirstAt = v.FirstAt
			}
			if v.LastAt != nil && (q.LastAt == nil || *v.LastAt > *q.LastAt) {
				q.LastAt = v.LastAt
			}
			if q.LineFrom == 0 || v.LineFrom < q.LineFrom {
				q.LineFrom = v.LineFrom
			}
			if v.LineTo > q.LineTo {
				q.LineTo = v.LineTo
			}
		} else {
			index[key] = len(out)
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}
