// 台站分组：把多台台站聚成逻辑组，便于按组查看整体健康度。
// 分组数据存放在独立的两张表（station_groups / station_group_members），
// 通过 EnsureTables 惰性建表，不侵入 store 包的既有迁移流程。
package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"seiswatch/internal/store"
)

// StationGroup 是一个命名的台站集合，StationCodes 按字典序返回。
type StationGroup struct {
	ID           int64
	Name         string
	StationCodes []string
	CreatedAt    time.Time
}

// GroupService 提供分组的增删查与组级健康度聚合。
type GroupService struct {
	db     *store.DB
	health *HealthService
}

// NewGroupService 组合 HealthService，用于 GroupHealth 逐台站打分。
func NewGroupService(db *store.DB) *GroupService {
	return &GroupService{db: db, health: NewHealthService(db)}
}

// EnsureTables 建立 station_groups 与 station_group_members 两张表。
// 幂等，可在服务启动时重复调用。
func EnsureTables(db *store.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS station_groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS station_group_members (
			group_id INTEGER NOT NULL REFERENCES station_groups(id),
			station_code TEXT NOT NULL,
			added_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (group_id, station_code)
		)`,
	}
	for _, s := range stmts {
		if _, err := db.SQL().Exec(s); err != nil {
			return fmt.Errorf("ensure group tables: %w", err)
		}
	}
	return nil
}

// Create 新建分组并写入初始成员。要求 name 非空、codes 非空，
// 且每个编码都必须已存在于 stations 表；成员编码自动大写去重。
func (s *GroupService) Create(name string, codes []string) (*StationGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("group name must not be empty")
	}
	if len(codes) == 0 {
		return nil, fmt.Errorf("group %q needs at least one station code", name)
	}
	ctx := context.Background()
	members, err := s.resolveMembers(ctx, codes)
	if err != nil {
		return nil, err
	}
	res, err := s.db.SQL().ExecContext(ctx,
		`INSERT INTO station_groups (name) VALUES (?)`, name)
	if err != nil {
		return nil, fmt.Errorf("insert station group %q: %w", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	for _, code := range members {
		if _, err := s.db.SQL().ExecContext(ctx,
			`INSERT INTO station_group_members (group_id, station_code) VALUES (?, ?)`,
			id, code); err != nil {
			return nil, fmt.Errorf("add member %s to group %d: %w", code, id, err)
		}
	}
	return s.Get(id)
}

// resolveMembers 校验并规范化成员编码：大写、去重、逐个确认存在。
func (s *GroupService) resolveMembers(ctx context.Context, codes []string) ([]string, error) {
	seen := make(map[string]bool, len(codes))
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		code := strings.ToUpper(strings.TrimSpace(c))
		if code == "" {
			return nil, fmt.Errorf("station code must not be empty")
		}
		if seen[code] {
			continue
		}
		if _, err := s.db.Stations.GetByCode(ctx, code); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("unknown station code %q", code)
			}
			return nil, err
		}
		seen[code] = true
		out = append(out, code)
	}
	return out, nil
}

// Get 按 ID 加载单个分组，含全部成员编码。
func (s *GroupService) Get(id int64) (*StationGroup, error) {
	var g StationGroup
	err := s.db.SQL().QueryRowContext(context.Background(),
		`SELECT id, name, created_at FROM station_groups WHERE id = ?`, id).
		Scan(&g.ID, &g.Name, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("station group %d not found", id)
	}
	if err != nil {
		return nil, err
	}
	codes, err := s.memberCodes(g.ID)
	if err != nil {
		return nil, err
	}
	g.StationCodes = codes
	return &g, nil
}

// memberCodes 返回分组内全部成员编码，按字典序稳定输出。
func (s *GroupService) memberCodes(groupID int64) ([]string, error) {
	rows, err := s.db.SQL().QueryContext(context.Background(),
		`SELECT station_code FROM station_group_members WHERE group_id = ? ORDER BY station_code`,
		groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var codes []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, rows.Err()
}

// AddStation 向已有分组追加一台台站；重复添加视为幂等成功。
func (s *GroupService) AddStation(groupID int64, code string) error {
	ctx := context.Background()
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return fmt.Errorf("station code must not be empty")
	}
	if _, err := s.Get(groupID); err != nil {
		return err
	}
	if _, err := s.db.Stations.GetByCode(ctx, code); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("unknown station code %q", code)
		}
		return err
	}
	if _, err := s.db.SQL().ExecContext(ctx,
		`INSERT OR IGNORE INTO station_group_members (group_id, station_code) VALUES (?, ?)`,
		groupID, code); err != nil {
		return fmt.Errorf("add station %s to group %d: %w", code, groupID, err)
	}
	return nil
}

// RemoveStation 从分组移除一台台站；成员不存在时报错而非静默成功。
func (s *GroupService) RemoveStation(groupID int64, code string) error {
	code = strings.ToUpper(strings.TrimSpace(code))
	res, err := s.db.SQL().ExecContext(context.Background(),
		`DELETE FROM station_group_members WHERE group_id = ? AND station_code = ?`,
		groupID, code)
	if err != nil {
		return fmt.Errorf("remove station %s from group %d: %w", code, groupID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("station %s is not a member of group %d", code, groupID)
	}
	return nil
}

// List 返回全部分组，按名称排序，每个分组都带完整成员列表。
func (s *GroupService) List() ([]StationGroup, error) {
	rows, err := s.db.SQL().QueryContext(context.Background(),
		`SELECT id FROM station_groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	out := make([]StationGroup, 0, len(ids))
	for _, id := range ids {
		g, err := s.Get(id)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, nil
}

// GroupHealth 对组内每台台站调用 HealthService.Score 并取平均分。
// factors 汇总了各台站的扣分原因（保留来源编码），供前端直接展示。
// 空分组的平均分定义为满分 100。
func (s *GroupService) GroupHealth(groupID int64, now time.Time) (float64, []string, error) {
	g, err := s.Get(groupID)
	if err != nil {
		return 0, nil, err
	}
	ctx := context.Background()
	total := 0.0
	var factors []string
	for _, code := range g.StationCodes {
		st, err := s.db.Stations.GetByCode(ctx, code)
		if err != nil {
			return 0, nil, fmt.Errorf("load station %s: %w", code, err)
		}
		score, reasons, err := s.health.Score(st.ID, now)
		if err != nil {
			return 0, nil, fmt.Errorf("score station %s: %w", code, err)
		}
		total += score
		for _, r := range reasons {
			factors = append(factors, fmt.Sprintf("%s: %s", code, r))
		}
	}
	return total / float64(len(g.StationCodes)), factors, nil
}
