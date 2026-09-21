package store

import (
	"context"
	"database/sql"
)

// Mapping 是两家机构术语之间的映射关系。
type Mapping struct {
	MappingID      string `json:"mapping_id"`
	TermA          string `json:"term_a"`
	TermB          string `json:"term_b"`
	InstitutionA   string `json:"institution_a"`
	InstitutionB   string `json:"institution_b"`
	CurrentVersion *int   `json:"current_version"` // 当前生效版本号，未生效为 null
	CreatedBy      string `json:"created_by"`
	CreatedAt      string `json:"created_at"`
}

// MappingVersion 是映射的一次修订；旧版本永不删除。
type MappingVersion struct {
	MappingID     string `json:"mapping_id"`
	VersionNo     int    `json:"version_no"`
	RelationType  string `json:"relation_type"`
	AmbiguityNote string `json:"ambiguity_note"`
	ChangeNote    string `json:"change_note"`
	Status        string `json:"status"` // pending | effective | superseded | rejected
	CreatedBy     string `json:"created_by"`
	CreatedAt     string `json:"created_at"`
	EffectiveAt   string `json:"effective_at,omitempty"`
}

// Confirmation 是一侧临床负责人对某版本的签署，只增不改。
type Confirmation struct {
	MappingID      string `json:"mapping_id"`
	VersionNo      int    `json:"version_no"`
	InstitutionRef string `json:"institution_ref"`
	Decision       string `json:"decision"` // confirm | reject
	SignedBy       string `json:"signed_by"`
	SignedAt       string `json:"signed_at"`
	Note           string `json:"note"`
}

// CreateMapping 登记映射及其首个待确认版本。
func (s *Store) CreateMapping(ctx context.Context, m *Mapping, v *MappingVersion) error {
	m.MappingID = newID("MAP")
	m.CreatedAt = now()
	v.MappingID = m.MappingID
	v.VersionNo = 1
	v.Status = "pending"
	v.CreatedAt = m.CreatedAt
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO mappings
		(mapping_id, term_a, term_b, institution_a, institution_b, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.MappingID, m.TermA, m.TermB, m.InstitutionA, m.InstitutionB, m.CreatedBy, m.CreatedAt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO mapping_versions
		(mapping_id, version_no, relation_type, ambiguity_note, change_note, status, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		v.MappingID, v.VersionNo, v.RelationType, v.AmbiguityNote, v.ChangeNote, v.Status, v.CreatedBy, v.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// GetMapping 按标识读取映射。
func (s *Store) GetMapping(ctx context.Context, mappingID string) (*Mapping, error) {
	row := s.db.QueryRowContext(ctx, `SELECT mapping_id, term_a, term_b, institution_a, institution_b,
		current_version, created_by, created_at FROM mappings WHERE mapping_id = ?`, mappingID)
	m := &Mapping{}
	var current sql.NullInt64
	err := row.Scan(&m.MappingID, &m.TermA, &m.TermB, &m.InstitutionA, &m.InstitutionB,
		&current, &m.CreatedBy, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if current.Valid {
		n := int(current.Int64)
		m.CurrentVersion = &n
	}
	return m, nil
}

// ListMappingsByTerm 返回涉及指定术语的全部映射。
func (s *Store) ListMappingsByTerm(ctx context.Context, termID string) ([]Mapping, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mapping_id, term_a, term_b, institution_a, institution_b,
		current_version, created_by, created_at FROM mappings WHERE term_a = ? OR term_b = ?`, termID, termID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Mapping
	for rows.Next() {
		var m Mapping
		var current sql.NullInt64
		if err := rows.Scan(&m.MappingID, &m.TermA, &m.TermB, &m.InstitutionA, &m.InstitutionB,
			&current, &m.CreatedBy, &m.CreatedAt); err != nil {
			return nil, err
		}
		if current.Valid {
			n := int(current.Int64)
			m.CurrentVersion = &n
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListVersions 返回映射的全部版本（含已被取代的历史版本），按版本号升序。
func (s *Store) ListVersions(ctx context.Context, mappingID string) ([]MappingVersion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mapping_id, version_no, relation_type, ambiguity_note,
		change_note, status, created_by, created_at, COALESCE(effective_at, '')
		FROM mapping_versions WHERE mapping_id = ? ORDER BY version_no`, mappingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MappingVersion
	for rows.Next() {
		var v MappingVersion
		if err := rows.Scan(&v.MappingID, &v.VersionNo, &v.RelationType, &v.AmbiguityNote,
			&v.ChangeNote, &v.Status, &v.CreatedBy, &v.CreatedAt, &v.EffectiveAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetVersion 读取指定版本。
func (s *Store) GetVersion(ctx context.Context, mappingID string, versionNo int) (*MappingVersion, error) {
	row := s.db.QueryRowContext(ctx, `SELECT mapping_id, version_no, relation_type, ambiguity_note,
		change_note, status, created_by, created_at, COALESCE(effective_at, '')
		FROM mapping_versions WHERE mapping_id = ? AND version_no = ?`, mappingID, versionNo)
	v := &MappingVersion{}
	err := row.Scan(&v.MappingID, &v.VersionNo, &v.RelationType, &v.AmbiguityNote,
		&v.ChangeNote, &v.Status, &v.CreatedBy, &v.CreatedAt, &v.EffectiveAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

// CreateVersion 在映射下新增待确认版本（如翻译被推翻后的修订）。
// 存在尚未处理的待确认版本时拒绝，避免多个版本同时在审。
func (s *Store) CreateVersion(ctx context.Context, v *MappingVersion) error {
	var pending int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mapping_versions
		WHERE mapping_id = ? AND status = 'pending'`, v.MappingID).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return ErrConflict
	}
	var maxVersion sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(version_no) FROM mapping_versions
		WHERE mapping_id = ?`, v.MappingID).Scan(&maxVersion); err != nil {
		return err
	}
	v.VersionNo = int(maxVersion.Int64) + 1
	v.Status = "pending"
	v.CreatedAt = now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO mapping_versions
		(mapping_id, version_no, relation_type, ambiguity_note, change_note, status, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		v.MappingID, v.VersionNo, v.RelationType, v.AmbiguityNote, v.ChangeNote, v.Status, v.CreatedBy, v.CreatedAt)
	return err
}

// AddConfirmation 记录一侧的签署；同一机构对同一版本只能签署一次。
func (s *Store) AddConfirmation(ctx context.Context, c *Confirmation) error {
	var existing int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mapping_confirmations
		WHERE mapping_id = ? AND version_no = ? AND institution_ref = ?`,
		c.MappingID, c.VersionNo, c.InstitutionRef).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return ErrConflict
	}
	c.SignedAt = now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO mapping_confirmations
		(mapping_id, version_no, institution_ref, decision, signed_by, signed_at, note)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.MappingID, c.VersionNo, c.InstitutionRef, c.Decision, c.SignedBy, c.SignedAt, c.Note)
	return err
}

// ListConfirmations 返回某版本下的全部签署。
func (s *Store) ListConfirmations(ctx context.Context, mappingID string, versionNo int) ([]Confirmation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mapping_id, version_no, institution_ref, decision,
		signed_by, signed_at, note FROM mapping_confirmations
		WHERE mapping_id = ? AND version_no = ? ORDER BY signed_at`, mappingID, versionNo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Confirmation
	for rows.Next() {
		var c Confirmation
		if err := rows.Scan(&c.MappingID, &c.VersionNo, &c.InstitutionRef, &c.Decision,
			&c.SignedBy, &c.SignedAt, &c.Note); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ActivateVersion 在双边签署齐全后使版本生效：旧生效版本置为 superseded（保留历史），
// 新版本记录生效时间并成为映射的当前版本。
func (s *Store) ActivateVersion(ctx context.Context, mappingID string, versionNo int) error {
	effectiveAt := now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE mapping_versions SET status = 'superseded'
		WHERE mapping_id = ? AND status = 'effective'`, mappingID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE mapping_versions SET status = 'effective', effective_at = ?
		WHERE mapping_id = ? AND version_no = ? AND status = 'pending'`, effectiveAt, mappingID, versionNo)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mappings SET current_version = ?
		WHERE mapping_id = ?`, versionNo, mappingID); err != nil {
		return err
	}
	return tx.Commit()
}

// RejectVersion 将待确认版本置为已否决。
func (s *Store) RejectVersion(ctx context.Context, mappingID string, versionNo int) error {
	res, err := s.db.ExecContext(ctx, `UPDATE mapping_versions SET status = 'rejected'
		WHERE mapping_id = ? AND version_no = ? AND status = 'pending'`, mappingID, versionNo)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrConflict
	}
	return nil
}
