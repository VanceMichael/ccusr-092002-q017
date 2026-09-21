package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// TermSubmission 是机构提交的本地字段定义摘要及授权元数据。
type TermSubmission struct {
	TermID             string   `json:"term_id"`
	InstitutionRef     string   `json:"institution_ref"`
	LocalCode          string   `json:"local_code"`
	DefinitionDigest   string   `json:"definition_digest"`
	Language           string   `json:"language"`
	Unit               string   `json:"unit"`
	AuthorizedPurposes []string `json:"authorized_purposes"`
	SubmittedBy        string   `json:"submitted_by"`
	SubmittedAt        string   `json:"submitted_at"`
	Status             string   `json:"status"` // active | withdrawn
	WithdrawnBy        string   `json:"withdrawn_by,omitempty"`
	WithdrawnAt        string   `json:"withdrawn_at,omitempty"`
	WithdrawalScope    string   `json:"withdrawal_scope,omitempty"`
}

// CreateTerm 登记一条术语提交，生成标识、时间与初始状态。
func (s *Store) CreateTerm(ctx context.Context, t *TermSubmission) error {
	t.TermID = newID("TERM")
	t.SubmittedAt = now()
	t.Status = "active"
	purposes, err := json.Marshal(t.AuthorizedPurposes)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO term_submissions
		(term_id, institution_ref, local_code, definition_digest, language, unit, authorized_purposes, submitted_by, submitted_at, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.TermID, t.InstitutionRef, t.LocalCode, t.DefinitionDigest, t.Language, t.Unit,
		string(purposes), t.SubmittedBy, t.SubmittedAt, t.Status)
	return err
}

// GetTerm 按标识读取术语提交。
func (s *Store) GetTerm(ctx context.Context, termID string) (*TermSubmission, error) {
	row := s.db.QueryRowContext(ctx, `SELECT term_id, institution_ref, local_code, definition_digest,
		language, unit, authorized_purposes, submitted_by, submitted_at, status,
		COALESCE(withdrawn_by, ''), COALESCE(withdrawn_at, ''), COALESCE(withdrawal_scope, '')
		FROM term_submissions WHERE term_id = ?`, termID)
	t := &TermSubmission{}
	var purposes string
	err := row.Scan(&t.TermID, &t.InstitutionRef, &t.LocalCode, &t.DefinitionDigest,
		&t.Language, &t.Unit, &purposes, &t.SubmittedBy, &t.SubmittedAt, &t.Status,
		&t.WithdrawnBy, &t.WithdrawnAt, &t.WithdrawalScope)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(purposes), &t.AuthorizedPurposes); err != nil {
		return nil, err
	}
	return t, nil
}

// WithdrawTerm 撤回术语提交：仅记录撤回人与范围，历史映射与目录不受影响。
func (s *Store) WithdrawTerm(ctx context.Context, termID, actor, scope string) (*TermSubmission, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE term_submissions
		SET status = 'withdrawn', withdrawn_by = ?, withdrawn_at = ?, withdrawal_scope = ?
		WHERE term_id = ? AND status = 'active'`, actor, now(), scope, termID)
	if err != nil {
		return nil, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		if _, err := s.GetTerm(ctx, termID); err != nil {
			return nil, ErrNotFound
		}
		return nil, ErrConflict
	}
	return s.GetTerm(ctx, termID)
}
