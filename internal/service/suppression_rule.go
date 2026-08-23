// 事件抑制规则：比维护窗口更细粒度的告警降噪手段，按
// (station_code, rule_id) 维度配置每天固定小时段的静默窗口。
// 典型场景是某台站某类规则（如 GAP）每天固定时段因已知干扰
// 必然误报，运维侧希望在该时段内不出事件、其余照常。
package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"seiswatch/internal/model"
	"seiswatch/internal/store"
)

// SuppressionRule 描述一条抑制规则：对 stationCode 台站的 ruleId
// 类事件，在每天的 [FromHour, ToHour) 小时段内不产生/不上报。
type SuppressionRule struct {
	ID          int64
	StationCode string
	RuleID      string
	FromHour    int // 含左端点，0~23
	ToHour      int // 不含右端点，1~24
	Reason      string
	Enabled     bool
}

// SuppressionService 管理抑制规则的持久化与命中判断。
type SuppressionService struct {
	db *store.DB
}

// NewSuppressionService 构造服务实例；建表请先调用 EnsureTables。
func NewSuppressionService(db *store.DB) *SuppressionService {
	return &SuppressionService{db: db}
}

// EnsureTables 建立 suppression_rules 表，幂等可重复调用。
func (s *SuppressionService) EnsureTables() error {
	stmt := `CREATE TABLE IF NOT EXISTS suppression_rules (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		station_code TEXT NOT NULL,
		rule_id TEXT NOT NULL,
		from_hour INTEGER NOT NULL,
		to_hour INTEGER NOT NULL,
		reason TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1,
		UNIQUE(station_code, rule_id)
	)`
	if _, err := s.db.SQL().Exec(stmt); err != nil {
		return fmt.Errorf("ensure suppression_rules: %w", err)
	}
	return nil
}

// Upsert 按 (StationCode, RuleID) 唯一键插入或整体覆盖一条规则，
// 返回规则 ID。校验失败时返回错误且不落库：
//   - RuleID 非空；
//   - StationCode 非空；
//   - 0 <= FromHour < 24 且 FromHour < ToHour <= 24。
func (s *SuppressionService) Upsert(rule SuppressionRule) (int64, error) {
	rule.StationCode = strings.ToUpper(strings.TrimSpace(rule.StationCode))
	rule.RuleID = strings.TrimSpace(rule.RuleID)
	if rule.StationCode == "" {
		return 0, fmt.Errorf("suppression rule needs a station code")
	}
	if rule.RuleID == "" {
		return 0, fmt.Errorf("suppression rule needs a non-empty rule id")
	}
	if err := validateHours(rule.FromHour, rule.ToHour); err != nil {
		return 0, err
	}
	ctx := context.Background()
	const q = `INSERT INTO suppression_rules (station_code, rule_id, from_hour, to_hour, reason, enabled)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(station_code, rule_id) DO UPDATE SET
	from_hour = excluded.from_hour,
	to_hour = excluded.to_hour,
	reason = excluded.reason,
	enabled = excluded.enabled`
	if _, err := s.db.SQL().ExecContext(ctx, q,
		rule.StationCode, rule.RuleID, rule.FromHour, rule.ToHour,
		strings.TrimSpace(rule.Reason), boolToInt(rule.Enabled)); err != nil {
		return 0, fmt.Errorf("upsert suppression rule %s/%s: %w", rule.StationCode, rule.RuleID, err)
	}
	var id int64
	err := s.db.SQL().QueryRowContext(ctx,
		`SELECT id FROM suppression_rules WHERE station_code = ? AND rule_id = ?`,
		rule.StationCode, rule.RuleID).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// validateHours 检查每日小时段约束；ToHour 允许取 24 表示覆盖到当天末尾。
func validateHours(from, to int) error {
	if from < 0 || from >= 24 {
		return fmt.Errorf("from hour %d outside [0, 24)", from)
	}
	if to <= from || to > 24 {
		return fmt.Errorf("to hour %d invalid, want (%d, 24]", to, from)
	}
	return nil
}

// Disable 按 ID 关闭规则（保留配置便于日后恢复）；不存在时报错。
func (s *SuppressionService) Disable(id int64) error {
	return s.setEnabled(id, false)
}

// Enable 按 ID 重新启用规则；不存在时报错。
func (s *SuppressionService) Enable(id int64) error {
	return s.setEnabled(id, true)
}

func (s *SuppressionService) setEnabled(id int64, enabled bool) error {
	res, err := s.db.SQL().ExecContext(context.Background(),
		`UPDATE suppression_rules SET enabled = ? WHERE id = ?`, boolToInt(enabled), id)
	if err != nil {
		return fmt.Errorf("set suppression rule %d enabled=%v: %w", id, enabled, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("suppression rule %d not found", id)
	}
	return nil
}

// Active 报告指定台站与规则的组合在 at 时刻是否处于抑制状态：
// 规则存在、已启用且 at.Hour() 落在 [FromHour, ToHour) 内。
// 查询出错按未抑制处理（fail-open），避免存储故障吞掉全部告警。
func (s *SuppressionService) Active(ruleID, stationCode string, at time.Time) bool {
	var (
		from, to int
		enabled  int
	)
	err := s.db.SQL().QueryRowContext(context.Background(),
		`SELECT from_hour, to_hour, enabled FROM suppression_rules
WHERE station_code = ? AND rule_id = ?`,
		strings.ToUpper(strings.TrimSpace(stationCode)), ruleID).
		Scan(&from, &to, &enabled)
	if err != nil {
		return false
	}
	return enabled == 1 && from < at.Hour() && at.Hour() <= to
}

// FilterEvents 过滤掉在 at 时刻被任一启用规则命中的事件。
// 存储层出错时 fail-open 返回原切片，保证告警链路不因辅助功能失效。
// 事件通过 StationID 关联台站编码后再匹配规则。
func (s *SuppressionService) FilterEvents(events []model.QCEvent, at time.Time) []model.QCEvent {
	rulesByStation, err := s.activeRulesAt(at)
	if err != nil {
		return events
	}
	codeByStation, err := s.stationCodes()
	if err != nil {
		return events
	}
	out := events[:0]
	for _, ev := range events {
		code := codeByStation[ev.StationID]
		hourRules := rulesByStation[code]
		if hourRules != nil && hourRules[ev.RuleID] {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// activeRulesAt 加载全部启用规则，返回 station -> ruleID -> 命中@at 的映射。
func (s *SuppressionService) activeRulesAt(at time.Time) (map[string]map[string]bool, error) {
	rules, err := s.list(true)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]bool, len(rules))
	for _, r := range rules {
		if !hourHits(r, at.Hour()) {
			continue
		}
		m := out[r.StationCode]
		if m == nil {
			m = make(map[string]bool)
			out[r.StationCode] = m
		}
		m[r.RuleID] = true
	}
	return out, nil
}

// hourHits 判断 hour 是否落在规则的 [FromHour, ToHour) 区间内。
func hourHits(r SuppressionRule, hour int) bool {
	return r.FromHour < hour && hour <= r.ToHour
}

// stationCodes 返回 station_id -> station_code 的全量映射。
func (s *SuppressionService) stationCodes() (map[int64]string, error) {
	stations, err := s.db.Stations.List(context.Background())
	if err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(stations))
	for i := range stations {
		out[stations[i].ID] = stations[i].Code
	}
	return out, nil
}

// List 返回全部规则（含禁用），按台站与规则 ID 排序，供管理端展示。
func (s *SuppressionService) List() ([]SuppressionRule, error) {
	return s.list(false)
}

func (s *SuppressionService) list(onlyEnabled bool) ([]SuppressionRule, error) {
	q := `SELECT id, station_code, rule_id, from_hour, to_hour, reason, enabled
FROM suppression_rules`
	if onlyEnabled {
		q += ` WHERE enabled = 1`
	}
	q += ` ORDER BY station_code, rule_id`
	rows, err := s.db.SQL().QueryContext(context.Background(), q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SuppressionRule
	for rows.Next() {
		var (
			r       SuppressionRule
			enabled int
		)
		if err := rows.Scan(&r.ID, &r.StationCode, &r.RuleID, &r.FromHour,
			&r.ToHour, &r.Reason, &enabled); err != nil {
			return nil, err
		}
		r.Enabled = enabled == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetByID 按 ID 加载单条规则；不存在时返回包装后的 ErrNotFound 语义错误。
func (s *SuppressionService) GetByID(id int64) (*SuppressionRule, error) {
	var (
		r       SuppressionRule
		enabled int
	)
	err := s.db.SQL().QueryRowContext(context.Background(),
		`SELECT id, station_code, rule_id, from_hour, to_hour, reason, enabled
FROM suppression_rules WHERE id = ?`, id).
		Scan(&r.ID, &r.StationCode, &r.RuleID, &r.FromHour, &r.ToHour, &r.Reason, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("suppression rule %d not found", id)
	}
	if err != nil {
		return nil, err
	}
	r.Enabled = enabled == 1
	return &r, nil
}

// boolToInt 把布尔值映射为 SQLite 的 0/1 整数列值。
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
