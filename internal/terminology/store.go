package terminology

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/vancemichael/092002-medical-collaboration-control/migrations"
	_ "modernc.org/sqlite"
)

// Store 封装 SQLite 持久化；所有写入经由 Service 的不变量校验。
type Store struct {
	db *sql.DB
}

// Open 打开（必要时创建）数据库文件并应用未执行的迁移。
func Open(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("创建数据目录失败: %w", err)
		}
		dsn = "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// SQLite 单写者，避免连接池放大锁竞争。
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close 关闭底层连接。
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return fmt.Errorf("读取迁移目录失败: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		version := name[:len(name)-len(".sql")]
		var applied int
		// 首个迁移执行前 schema_migrations 尚不存在，查询失败即视为未应用。
		_ = s.db.QueryRow("SELECT 1 FROM schema_migrations WHERE version = ?", version).Scan(&applied)
		if applied == 1 {
			continue
		}
		script, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("读取迁移 %s 失败: %w", name, err)
		}
		if _, err := s.db.Exec(string(script)); err != nil {
			return fmt.Errorf("应用迁移 %s 失败: %w", name, err)
		}
	}
	return nil
}

func (s *Store) insertSubmission(sub *Submission) error {
	purposes, err := json.Marshal(sub.Purposes)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO term_submissions
		 (submission_ref, institution_ref, local_field_ref, definition_digest, language, unit, purposes, submitted_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sub.SubmissionRef, sub.InstitutionRef, sub.LocalFieldRef, sub.DefinitionDigest,
		sub.Language, sub.Unit, string(purposes), sub.SubmittedAt,
	)
	return err
}

func scanSubmission(row interface{ Scan(...any) error }) (*Submission, error) {
	var sub Submission
	var purposes string
	err := row.Scan(&sub.SubmissionRef, &sub.InstitutionRef, &sub.LocalFieldRef,
		&sub.DefinitionDigest, &sub.Language, &sub.Unit, &purposes, &sub.SubmittedAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(purposes), &sub.Purposes); err != nil {
		return nil, err
	}
	return &sub, nil
}

const submissionColumns = "submission_ref, institution_ref, local_field_ref, definition_digest, language, unit, purposes, submitted_at"

func (s *Store) getSubmission(ref string) (*Submission, error) {
	row := s.db.QueryRow("SELECT "+submissionColumns+" FROM term_submissions WHERE submission_ref = ?", ref)
	return scanSubmission(row)
}

func (s *Store) listSubmissions(institution string) ([]Submission, error) {
	rows, err := s.db.Query(
		"SELECT "+submissionColumns+" FROM term_submissions WHERE institution_ref = ? ORDER BY rowid",
		institution)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Submission{}
	for rows.Next() {
		sub, err := scanSubmission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sub)
	}
	return out, rows.Err()
}

func (s *Store) insertMapping(m *Mapping, v *Version) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		"INSERT INTO mappings (mapping_ref, indicator_key, created_at) VALUES (?, ?, ?)",
		m.MappingRef, m.IndicatorKey, m.CreatedAt); err != nil {
		return err
	}
	if err := insertVersionTx(tx, v); err != nil {
		return err
	}
	return tx.Commit()
}

func insertVersionTx(tx *sql.Tx, v *Version) error {
	_, err := tx.Exec(
		`INSERT INTO mapping_versions
		 (mapping_ref, version, left_submission_ref, right_submission_ref, equivalence_note, status, status_reason, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		v.MappingRef, v.Version, v.LeftSubmissionRef, v.RightSubmissionRef,
		v.EquivalenceNote, v.Status, v.StatusReason, v.CreatedAt)
	return err
}

func (s *Store) nextVersionTx(tx *sql.Tx, mappingRef string) (int, error) {
	var max sql.NullInt64
	if err := tx.QueryRow(
		"SELECT MAX(version) FROM mapping_versions WHERE mapping_ref = ?", mappingRef).Scan(&max); err != nil {
		return 0, err
	}
	if !max.Valid {
		return 0, errNotFound("映射不存在")
	}
	return int(max.Int64) + 1, nil
}

func (s *Store) insertVersion(v *Version) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertVersionTx(tx, v); err != nil {
		return err
	}
	return tx.Commit()
}

func scanVersion(row interface{ Scan(...any) error }) (*Version, error) {
	var v Version
	err := row.Scan(&v.MappingRef, &v.Version, &v.LeftSubmissionRef, &v.RightSubmissionRef,
		&v.EquivalenceNote, &v.Status, &v.StatusReason, &v.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

const versionColumns = "mapping_ref, version, left_submission_ref, right_submission_ref, equivalence_note, status, status_reason, created_at"

func (s *Store) getVersion(mappingRef string, version int) (*Version, error) {
	row := s.db.QueryRow(
		"SELECT "+versionColumns+" FROM mapping_versions WHERE mapping_ref = ? AND version = ?",
		mappingRef, version)
	return scanVersion(row)
}

func (s *Store) listVersions(mappingRef string) ([]Version, error) {
	rows, err := s.db.Query(
		"SELECT "+versionColumns+" FROM mapping_versions WHERE mapping_ref = ? ORDER BY version",
		mappingRef)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func (s *Store) getMapping(ref string) (*Mapping, error) {
	var m Mapping
	err := s.db.QueryRow(
		"SELECT mapping_ref, indicator_key, created_at FROM mappings WHERE mapping_ref = ?", ref).
		Scan(&m.MappingRef, &m.IndicatorKey, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) listMappingsByIndicator(indicatorKey string) ([]Mapping, error) {
	rows, err := s.db.Query(
		"SELECT mapping_ref, indicator_key, created_at FROM mappings WHERE indicator_key = ? ORDER BY rowid",
		indicatorKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Mapping{}
	for rows.Next() {
		var m Mapping
		if err := rows.Scan(&m.MappingRef, &m.IndicatorKey, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// confirmAndMaybeActivate 写入签署；若双方均已签署则把版本推进为 effective。
// 返回最新的版本状态。
func (s *Store) confirmAndMaybeActivate(c *Confirmation, parties []string) (*Version, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`INSERT INTO confirmations
		 (confirmation_ref, mapping_ref, version, institution_ref, signatory_ref, signed_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		c.ConfirmationRef, c.MappingRef, c.Version, c.InstitutionRef, c.SignatoryRef, c.SignedAt); err != nil {
		return nil, err
	}
	rows, err := tx.Query(
		"SELECT DISTINCT institution_ref FROM confirmations WHERE mapping_ref = ? AND version = ?",
		c.MappingRef, c.Version)
	if err != nil {
		return nil, err
	}
	confirmed := map[string]bool{}
	for rows.Next() {
		var inst string
		if err := rows.Scan(&inst); err != nil {
			rows.Close()
			return nil, err
		}
		confirmed[inst] = true
	}
	rows.Close()
	allConfirmed := true
	for _, party := range parties {
		if !confirmed[party] {
			allConfirmed = false
			break
		}
	}
	if allConfirmed {
		if _, err := tx.Exec(
			"UPDATE mapping_versions SET status = ? WHERE mapping_ref = ? AND version = ? AND status = ?",
			StatusEffective, c.MappingRef, c.Version, StatusPending); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.getVersion(c.MappingRef, c.Version)
}

func (s *Store) setVersionStatus(mappingRef string, version int, from []string, to, reason string) error {
	placeholders := ""
	args := []any{to, reason, mappingRef, version}
	for i, st := range from {
		if i > 0 {
			placeholders += ", "
		}
		placeholders += "?"
		args = append(args, st)
	}
	result, err := s.db.Exec(
		"UPDATE mapping_versions SET status = ?, status_reason = ? WHERE mapping_ref = ? AND version = ? AND status IN ("+placeholders+")",
		args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errConflict("版本状态已变化，无法执行该操作")
	}
	return nil
}

func (s *Store) listConfirmations(mappingRef string, version int) ([]Confirmation, error) {
	rows, err := s.db.Query(
		`SELECT confirmation_ref, mapping_ref, version, institution_ref, signatory_ref, signed_at
		 FROM confirmations WHERE mapping_ref = ? AND version = ? ORDER BY rowid`,
		mappingRef, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Confirmation{}
	for rows.Next() {
		var c Confirmation
		if err := rows.Scan(&c.ConfirmationRef, &c.MappingRef, &c.Version,
			&c.InstitutionRef, &c.SignatoryRef, &c.SignedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) insertWithdrawal(w *Withdrawal) error {
	scope, err := json.Marshal(w.Scope)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO withdrawals
		 (withdrawal_ref, mapping_ref, version, institution_ref, scope, reason, withdrawn_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		w.WithdrawalRef, w.MappingRef, w.Version, w.InstitutionRef, string(scope), w.Reason, w.WithdrawnAt)
	return err
}

func (s *Store) listWithdrawals(mappingRef string, version int) ([]Withdrawal, error) {
	rows, err := s.db.Query(
		`SELECT withdrawal_ref, mapping_ref, version, institution_ref, scope, reason, withdrawn_at
		 FROM withdrawals WHERE mapping_ref = ? AND version = ? ORDER BY rowid`,
		mappingRef, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Withdrawal{}
	for rows.Next() {
		var w Withdrawal
		var scope string
		if err := rows.Scan(&w.WithdrawalRef, &w.MappingRef, &w.Version,
			&w.InstitutionRef, &scope, &w.Reason, &w.WithdrawnAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(scope), &w.Scope); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) insertGrant(g *Grant) error {
	_, err := s.db.Exec(
		`INSERT INTO catalog_grants
		 (grant_ref, task_ref, indicator_key, mapping_ref, version, issued_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		g.GrantRef, g.TaskRef, g.IndicatorKey, g.MappingRef, g.Version, g.IssuedAt)
	return err
}

func (s *Store) listGrantsByTask(taskRef string) ([]Grant, error) {
	rows, err := s.db.Query(
		`SELECT grant_ref, task_ref, indicator_key, mapping_ref, version, issued_at
		 FROM catalog_grants WHERE task_ref = ? ORDER BY rowid`,
		taskRef)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGrants(rows)
}

func (s *Store) listGrantsByVersion(mappingRef string, version int) ([]Grant, error) {
	rows, err := s.db.Query(
		`SELECT grant_ref, task_ref, indicator_key, mapping_ref, version, issued_at
		 FROM catalog_grants WHERE mapping_ref = ? AND version = ? ORDER BY rowid`,
		mappingRef, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGrants(rows)
}

func scanGrants(rows *sql.Rows) ([]Grant, error) {
	out := []Grant{}
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.GrantRef, &g.TaskRef, &g.IndicatorKey,
			&g.MappingRef, &g.Version, &g.IssuedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) insertNote(n *AmbiguityNote) error {
	_, err := s.db.Exec(
		`INSERT INTO ambiguity_notes
		 (note_ref, mapping_ref, version, author_institution_ref, body, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		n.NoteRef, n.MappingRef, n.Version, n.AuthorInstitutionRef, n.Body, n.CreatedAt)
	return err
}

func (s *Store) listNotes(mappingRef string, version int) ([]AmbiguityNote, error) {
	rows, err := s.db.Query(
		`SELECT note_ref, mapping_ref, version, author_institution_ref, body, created_at
		 FROM ambiguity_notes WHERE mapping_ref = ? AND version = ? ORDER BY rowid`,
		mappingRef, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AmbiguityNote{}
	for rows.Next() {
		var n AmbiguityNote
		if err := rows.Scan(&n.NoteRef, &n.MappingRef, &n.Version,
			&n.AuthorInstitutionRef, &n.Body, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
