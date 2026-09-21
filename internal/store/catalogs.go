package store

import (
	"context"
	"database/sql"
)

// Catalog 是向指定协作任务发放的指标目录，发放后不可变。
type Catalog struct {
	CatalogID string `json:"catalog_id"`
	TaskRef   string `json:"task_ref"`
	IssuedBy  string `json:"issued_by"`
	IssuedAt  string `json:"issued_at"`
}

// CatalogEntry 绑定发放时的生效映射版本；版本日后被取代不影响本记录。
type CatalogEntry struct {
	CatalogID    string `json:"catalog_id"`
	IndicatorKey string `json:"indicator_key"`
	IndicatorRef string `json:"indicator_ref"`
	MappingID    string `json:"mapping_id"`
	VersionNo    int    `json:"version_no"`
}

// CreateCatalog 登记目录及其全部条目，并为每条目生成全局指标引用。
func (s *Store) CreateCatalog(ctx context.Context, c *Catalog, entries []CatalogEntry) error {
	c.CatalogID = newID("CAT")
	c.IssuedAt = now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO catalogs (catalog_id, task_ref, issued_by, issued_at)
		VALUES (?, ?, ?, ?)`, c.CatalogID, c.TaskRef, c.IssuedBy, c.IssuedAt); err != nil {
		return err
	}
	for i := range entries {
		entries[i].CatalogID = c.CatalogID
		entries[i].IndicatorRef = c.CatalogID + "/" + entries[i].IndicatorKey
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_entries
			(catalog_id, indicator_key, indicator_ref, mapping_id, version_no)
			VALUES (?, ?, ?, ?, ?)`,
			entries[i].CatalogID, entries[i].IndicatorKey, entries[i].IndicatorRef,
			entries[i].MappingID, entries[i].VersionNo); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetCatalog 读取目录及其条目。
func (s *Store) GetCatalog(ctx context.Context, catalogID string) (*Catalog, []CatalogEntry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT catalog_id, task_ref, issued_by, issued_at
		FROM catalogs WHERE catalog_id = ?`, catalogID)
	c := &Catalog{}
	err := row.Scan(&c.CatalogID, &c.TaskRef, &c.IssuedBy, &c.IssuedAt)
	if err == sql.ErrNoRows {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	entries, err := s.ListEntries(ctx, catalogID)
	if err != nil {
		return nil, nil, err
	}
	return c, entries, nil
}

// ListCatalogsByTask 返回指定协作任务的全部目录。
func (s *Store) ListCatalogsByTask(ctx context.Context, taskRef string) ([]Catalog, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT catalog_id, task_ref, issued_by, issued_at
		FROM catalogs WHERE task_ref = ? ORDER BY issued_at`, taskRef)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Catalog
	for rows.Next() {
		var c Catalog
		if err := rows.Scan(&c.CatalogID, &c.TaskRef, &c.IssuedBy, &c.IssuedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListEntries 返回目录的全部条目。
func (s *Store) ListEntries(ctx context.Context, catalogID string) ([]CatalogEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT catalog_id, indicator_key, indicator_ref, mapping_id, version_no
		FROM catalog_entries WHERE catalog_id = ? ORDER BY indicator_key`, catalogID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CatalogEntry
	for rows.Next() {
		var e CatalogEntry
		if err := rows.Scan(&e.CatalogID, &e.IndicatorKey, &e.IndicatorRef, &e.MappingID, &e.VersionNo); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetEntryByIndicatorRef 按全局指标引用定位目录条目，供监管追溯使用。
func (s *Store) GetEntryByIndicatorRef(ctx context.Context, indicatorRef string) (*CatalogEntry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT catalog_id, indicator_key, indicator_ref, mapping_id, version_no
		FROM catalog_entries WHERE indicator_ref = ?`, indicatorRef)
	e := &CatalogEntry{}
	err := row.Scan(&e.CatalogID, &e.IndicatorKey, &e.IndicatorRef, &e.MappingID, &e.VersionNo)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}
