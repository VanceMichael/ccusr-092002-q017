package terminology

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Kind 区分错误类别，HTTP 层据此映射状态码。
type Kind int

const (
	KindValidation Kind = iota // 输入不合法
	KindForbidden              // 身份或授权范围不符
	KindNotFound               // 引用的对象不存在
	KindConflict               // 与既有状态冲突（如重复签署、状态已推进）
)

// Error 是领域层错误，Msg 面向调用方。
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func errValidation(format string, args ...any) *Error {
	return &Error{Kind: KindValidation, Msg: fmt.Sprintf(format, args...)}
}
func errForbidden(format string, args ...any) *Error {
	return &Error{Kind: KindForbidden, Msg: fmt.Sprintf(format, args...)}
}
func errNotFound(format string, args ...any) *Error {
	return &Error{Kind: KindNotFound, Msg: fmt.Sprintf(format, args...)}
}
func errConflict(format string, args ...any) *Error {
	return &Error{Kind: KindConflict, Msg: fmt.Sprintf(format, args...)}
}

// digestPattern 约束定义摘要只能是 sha256 十六进制，中心不接收定义原文。
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Service 承载术语协议的全部用例。
type Service struct {
	store *Store
}

// NewService 基于已有存储构造服务。
func NewService(store *Store) *Service { return &Service{store: store} }

func newRef(prefix string) (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(buf), nil
}

func checkLen(name, value string, min, max int) *Error {
	n := utf8.RuneCountInString(strings.TrimSpace(value))
	if n < min {
		return errValidation("%s 不能为空", name)
	}
	if n > max {
		return errValidation("%s 长度不能超过 %d 字", name, max)
	}
	return nil
}

func checkRef(name, value string) *Error {
	if err := checkLen(name, value, 1, 128); err != nil {
		return err
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return errValidation("%s 不能包含控制字符", name)
		}
	}
	return nil
}

// requireRole 校验调用方角色。
func requireRole(actor Actor, roles ...string) *Error {
	for _, role := range roles {
		if actor.Role == role {
			return nil
		}
	}
	return errForbidden("角色 %s 无权执行该操作", actor.Role)
}

// SubmitTermInput 是提交字段定义摘要的入参。
type SubmitTermInput struct {
	InstitutionRef   string   `json:"institution_ref"`
	LocalFieldRef    string   `json:"local_field_ref"`
	DefinitionDigest string   `json:"definition_digest"`
	Language         string   `json:"language"`
	Unit             string   `json:"unit"`
	Purposes         []string `json:"purposes"`
}

// SubmitTerm 登记机构本地字段定义的不可逆摘要及授权用途。
func (s *Service) SubmitTerm(actor Actor, in SubmitTermInput) (*Submission, error) {
	if err := requireRole(actor, RoleDataSteward); err != nil {
		return nil, err
	}
	if in.InstitutionRef != actor.Institution {
		return nil, errForbidden("只能为本机构提交字段定义")
	}
	if err := checkRef("institution_ref", in.InstitutionRef); err != nil {
		return nil, err
	}
	if err := checkRef("local_field_ref", in.LocalFieldRef); err != nil {
		return nil, err
	}
	if !digestPattern.MatchString(in.DefinitionDigest) {
		return nil, errValidation("definition_digest 必须是 sha256:<64 位小写十六进制> 的不可逆摘要")
	}
	if err := checkLen("language", in.Language, 2, 35); err != nil {
		return nil, err
	}
	if err := checkLen("unit", in.Unit, 0, 32); err != nil {
		return nil, err
	}
	if len(in.Purposes) == 0 || len(in.Purposes) > 8 {
		return nil, errValidation("purposes 需要 1 到 8 项授权用途")
	}
	for _, purpose := range in.Purposes {
		if err := checkLen("purposes 项", purpose, 1, 64); err != nil {
			return nil, err
		}
	}
	ref, err := newRef("TS")
	if err != nil {
		return nil, err
	}
	sub := &Submission{
		SubmissionRef:    ref,
		InstitutionRef:   in.InstitutionRef,
		LocalFieldRef:    in.LocalFieldRef,
		DefinitionDigest: in.DefinitionDigest,
		Language:         in.Language,
		Unit:             in.Unit,
		Purposes:         in.Purposes,
		SubmittedAt:      now(),
	}
	if err := s.store.insertSubmission(sub); err != nil {
		if isUniqueViolation(err) {
			return nil, errConflict("该机构已存在同名 local_field_ref，如需更新请换用新的字段标识")
		}
		return nil, err
	}
	return sub, nil
}

// ListSubmissions 列出机构自己的提交；牵头中心与监管可查看任意机构。
func (s *Service) ListSubmissions(actor Actor, institution string) ([]Submission, error) {
	if institution == "" {
		institution = actor.Institution
	}
	if institution != actor.Institution &&
		actor.Role != RoleLeadCenter && actor.Role != RoleRegulator {
		return nil, errForbidden("只能查看本机构提交的字段定义")
	}
	return s.store.listSubmissions(institution)
}

// ProposeMappingInput 发起一个映射（首个版本）。
type ProposeMappingInput struct {
	IndicatorKey       string `json:"indicator_key"`
	LeftSubmissionRef  string `json:"left_submission_ref"`
	RightSubmissionRef string `json:"right_submission_ref"`
	EquivalenceNote    string `json:"equivalence_note"`
}

// ProposeVersionInput 在既有映射下追加新版本。
type ProposeVersionInput struct {
	LeftSubmissionRef  string `json:"left_submission_ref"`
	RightSubmissionRef string `json:"right_submission_ref"`
	EquivalenceNote    string `json:"equivalence_note"`
}

// ProposeMapping 建立映射并创建待确认的第 1 版。
func (s *Service) ProposeMapping(actor Actor, in ProposeMappingInput) (*Mapping, *Version, error) {
	if err := requireRole(actor, RoleDataSteward); err != nil {
		return nil, nil, err
	}
	if err := checkRef("indicator_key", in.IndicatorKey); err != nil {
		return nil, nil, err
	}
	left, right, err := s.checkSubmissionPair(actor, in.LeftSubmissionRef, in.RightSubmissionRef)
	if err != nil {
		return nil, nil, err
	}
	if err := checkLen("equivalence_note", in.EquivalenceNote, 1, 1000); err != nil {
		return nil, nil, err
	}
	mappingRef, err := newRef("MP")
	if err != nil {
		return nil, nil, err
	}
	mapping := &Mapping{MappingRef: mappingRef, IndicatorKey: in.IndicatorKey, CreatedAt: now()}
	version := &Version{
		MappingRef:         mappingRef,
		Version:            1,
		LeftSubmissionRef:  left.SubmissionRef,
		RightSubmissionRef: right.SubmissionRef,
		EquivalenceNote:    in.EquivalenceNote,
		Status:             StatusPending,
		CreatedAt:          now(),
	}
	if err := s.store.insertMapping(mapping, version); err != nil {
		return nil, nil, err
	}
	return mapping, version, nil
}

// ProposeVersion 在既有映射下追加新版本（例如翻译被推翻后的修订）。
// 同一映射两侧的机构集合必须保持不变。
func (s *Service) ProposeVersion(actor Actor, mappingRef string, in ProposeVersionInput) (*Version, error) {
	if err := requireRole(actor, RoleDataSteward); err != nil {
		return nil, err
	}
	mapping, err := s.store.getMapping(mappingRef)
	if err != nil {
		return nil, notFoundIfMissing(err, "映射不存在")
	}
	left, right, err := s.checkSubmissionPair(actor, in.LeftSubmissionRef, in.RightSubmissionRef)
	if err != nil {
		return nil, err
	}
	if err := checkLen("equivalence_note", in.EquivalenceNote, 1, 1000); err != nil {
		return nil, err
	}
	versions, err := s.store.listVersions(mapping.MappingRef)
	if err != nil {
		return nil, err
	}
	if err := s.checkSameParties(versions, left, right); err != nil {
		return nil, err
	}
	version := &Version{
		MappingRef:         mapping.MappingRef,
		Version:            len(versions) + 1,
		LeftSubmissionRef:  left.SubmissionRef,
		RightSubmissionRef: right.SubmissionRef,
		EquivalenceNote:    in.EquivalenceNote,
		Status:             StatusPending,
		CreatedAt:          now(),
	}
	if err := s.store.insertVersion(version); err != nil {
		return nil, err
	}
	return version, nil
}

// checkSubmissionPair 校验两侧提交存在、分属不同机构，且调用方是其中一方。
func (s *Service) checkSubmissionPair(actor Actor, leftRef, rightRef string) (*Submission, *Submission, error) {
	if leftRef == rightRef {
		return nil, nil, errValidation("映射两侧不能使用同一份提交")
	}
	left, err := s.store.getSubmission(leftRef)
	if err != nil {
		return nil, nil, notFoundIfMissing(err, "left_submission_ref 不存在")
	}
	right, err := s.store.getSubmission(rightRef)
	if err != nil {
		return nil, nil, notFoundIfMissing(err, "right_submission_ref 不存在")
	}
	if left.InstitutionRef == right.InstitutionRef {
		return nil, nil, errValidation("映射两侧必须分属不同机构")
	}
	if actor.Institution != left.InstitutionRef && actor.Institution != right.InstitutionRef {
		return nil, nil, errForbidden("只能就本机构参与的提交发起映射")
	}
	return left, right, nil
}

// checkSameParties 保证新版本的两侧机构与既有版本一致。
func (s *Service) checkSameParties(versions []Version, left, right *Submission) error {
	if len(versions) == 0 {
		return nil
	}
	first, err := s.versionParties(versions[0])
	if err != nil {
		return err
	}
	current := map[string]bool{left.InstitutionRef: true, right.InstitutionRef: true}
	for _, party := range first {
		if !current[party] {
			return errValidation("同一映射的两侧机构必须保持一致，如需更换机构请新建映射")
		}
	}
	return nil
}

// versionParties 返回一个版本两侧提交所属的机构。
func (s *Service) versionParties(v Version) ([]string, error) {
	left, err := s.store.getSubmission(v.LeftSubmissionRef)
	if err != nil {
		return nil, err
	}
	right, err := s.store.getSubmission(v.RightSubmissionRef)
	if err != nil {
		return nil, err
	}
	return []string{left.InstitutionRef, right.InstitutionRef}, nil
}

// requireParty 要求调用方机构是该版本的当事方。
func (s *Service) requireParty(actor Actor, v *Version) ([]string, error) {
	parties, err := s.versionParties(*v)
	if err != nil {
		return nil, err
	}
	for _, party := range parties {
		if party == actor.Institution {
			return parties, nil
		}
	}
	return nil, errForbidden("本机构不是该映射的当事方")
}

// Confirm 由一方临床负责人签署当前版本；双方签署齐全后版本生效。
func (s *Service) Confirm(actor Actor, mappingRef string, versionNo int, signatoryRef string) (*Version, error) {
	if err := requireRole(actor, RoleClinicalLead); err != nil {
		return nil, err
	}
	if signatoryRef == "" {
		signatoryRef = "clinical-lead:" + actor.Institution
	}
	if err := checkRef("signatory_ref", signatoryRef); err != nil {
		return nil, err
	}
	version, err := s.store.getVersion(mappingRef, versionNo)
	if err != nil {
		return nil, notFoundIfMissing(err, "映射版本不存在")
	}
	parties, err := s.requireParty(actor, version)
	if err != nil {
		return nil, err
	}
	if version.Status != StatusPending {
		return nil, errConflict("版本状态为 %s，不能签署", version.Status)
	}
	ref, err := newRef("CF")
	if err != nil {
		return nil, err
	}
	confirmation := &Confirmation{
		ConfirmationRef: ref,
		MappingRef:      mappingRef,
		Version:         versionNo,
		InstitutionRef:  actor.Institution,
		SignatoryRef:    signatoryRef,
		SignedAt:        now(),
	}
	updated, err := s.store.confirmAndMaybeActivate(confirmation, parties)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, errConflict("本机构已签署过该版本")
		}
		return nil, err
	}
	return updated, nil
}

// Overturn 推翻一个版本的术语翻译；历史版本与已发放记录保留，仅状态推进。
func (s *Service) Overturn(actor Actor, mappingRef string, versionNo int, reason string) (*Version, error) {
	if err := requireRole(actor, RoleClinicalLead); err != nil {
		return nil, err
	}
	if err := checkLen("reason", reason, 1, 500); err != nil {
		return nil, err
	}
	version, err := s.store.getVersion(mappingRef, versionNo)
	if err != nil {
		return nil, notFoundIfMissing(err, "映射版本不存在")
	}
	if _, err := s.requireParty(actor, version); err != nil {
		return nil, err
	}
	if version.Status == StatusOverturned {
		return nil, errConflict("版本已被推翻")
	}
	if err := s.store.setVersionStatus(mappingRef, versionNo,
		[]string{StatusPending, StatusEffective}, StatusOverturned, reason); err != nil {
		return nil, err
	}
	return s.store.getVersion(mappingRef, versionNo)
}

// Withdraw 登记机构对某版本的撤回及范围；不删除任何历史记录。
func (s *Service) Withdraw(actor Actor, mappingRef string, versionNo int, scope WithdrawalScope, reason string) (*Withdrawal, error) {
	if err := requireRole(actor, RoleClinicalLead); err != nil {
		return nil, err
	}
	if err := checkLen("reason", reason, 1, 500); err != nil {
		return nil, err
	}
	if err := checkScope(scope); err != nil {
		return nil, err
	}
	version, err := s.store.getVersion(mappingRef, versionNo)
	if err != nil {
		return nil, notFoundIfMissing(err, "映射版本不存在")
	}
	if _, err := s.requireParty(actor, version); err != nil {
		return nil, err
	}
	if version.Status == StatusOverturned {
		return nil, errConflict("版本已被推翻，无需撤回")
	}
	ref, err := newRef("WD")
	if err != nil {
		return nil, err
	}
	withdrawal := &Withdrawal{
		WithdrawalRef:  ref,
		MappingRef:     mappingRef,
		Version:        versionNo,
		InstitutionRef: actor.Institution,
		Scope:          scope,
		Reason:         reason,
		WithdrawnAt:    now(),
	}
	if err := s.store.insertWithdrawal(withdrawal); err != nil {
		return nil, err
	}
	return withdrawal, nil
}

func checkScope(scope WithdrawalScope) *Error {
	if len(scope.Tasks) > 64 || len(scope.Purposes) > 64 {
		return errValidation("撤回范围列表过长")
	}
	for _, task := range scope.Tasks {
		if err := checkRef("scope.tasks 项", task); err != nil {
			return err
		}
	}
	for _, purpose := range scope.Purposes {
		if err := checkLen("scope.purposes 项", purpose, 1, 64); err != nil {
			return err
		}
	}
	if scope.From != "" {
		if _, err := time.Parse(time.RFC3339, scope.From); err != nil {
			return errValidation("scope.from 必须是带时区偏移的 ISO 8601 时间")
		}
	}
	return nil
}

// AddAmbiguityNote 由当事方登记某版本的歧义说明。
func (s *Service) AddAmbiguityNote(actor Actor, mappingRef string, versionNo int, body string) (*AmbiguityNote, error) {
	if err := requireRole(actor, RoleDataSteward, RoleClinicalLead); err != nil {
		return nil, err
	}
	if err := checkLen("body", body, 1, 2000); err != nil {
		return nil, err
	}
	version, err := s.store.getVersion(mappingRef, versionNo)
	if err != nil {
		return nil, notFoundIfMissing(err, "映射版本不存在")
	}
	if _, err := s.requireParty(actor, version); err != nil {
		return nil, err
	}
	ref, err := newRef("AN")
	if err != nil {
		return nil, err
	}
	note := &AmbiguityNote{
		NoteRef:              ref,
		MappingRef:           mappingRef,
		Version:              versionNo,
		AuthorInstitutionRef: actor.Institution,
		Body:                 body,
		CreatedAt:            now(),
	}
	if err := s.store.insertNote(note); err != nil {
		return nil, err
	}
	return note, nil
}

// GrantCatalog 由牵头中心把生效版本发放到指定协作任务。
func (s *Service) GrantCatalog(actor Actor, taskRef, indicatorKey, mappingRef string, versionNo int) (*Grant, error) {
	if err := requireRole(actor, RoleLeadCenter); err != nil {
		return nil, err
	}
	if err := checkRef("task_ref", taskRef); err != nil {
		return nil, err
	}
	mapping, err := s.store.getMapping(mappingRef)
	if err != nil {
		return nil, notFoundIfMissing(err, "映射不存在")
	}
	if mapping.IndicatorKey != indicatorKey {
		return nil, errValidation("indicator_key 与映射不一致")
	}
	version, err := s.store.getVersion(mappingRef, versionNo)
	if err != nil {
		return nil, notFoundIfMissing(err, "映射版本不存在")
	}
	if version.Status != StatusEffective {
		return nil, errConflict("只有双方临床负责人确认生效的版本才能发放")
	}
	ref, err := newRef("CG")
	if err != nil {
		return nil, err
	}
	grant := &Grant{
		GrantRef:     ref,
		TaskRef:      taskRef,
		IndicatorKey: indicatorKey,
		MappingRef:   mappingRef,
		Version:      versionNo,
		IssuedAt:     now(),
	}
	if err := s.store.insertGrant(grant); err != nil {
		return nil, err
	}
	return grant, nil
}

// TaskCatalog 返回指定任务的指标目录，只包含调用方机构参与的映射。
func (s *Service) TaskCatalog(actor Actor, taskRef string) (*Catalog, error) {
	if err := requireRole(actor, RoleDataSteward, RoleClinicalLead, RoleLeadCenter); err != nil {
		return nil, err
	}
	grants, err := s.store.listGrantsByTask(taskRef)
	if err != nil {
		return nil, err
	}
	// 同一指标多次发放时只展示最近一次。
	latest := map[string]Grant{}
	order := []string{}
	for _, grant := range grants {
		if _, seen := latest[grant.IndicatorKey]; !seen {
			order = append(order, grant.IndicatorKey)
		}
		latest[grant.IndicatorKey] = grant
	}
	catalog := &Catalog{TaskRef: taskRef, Entries: []CatalogEntry{}}
	for _, key := range order {
		grant := latest[key]
		entry, visible, err := s.buildEntry(actor, grant)
		if err != nil {
			return nil, err
		}
		if visible {
			catalog.Entries = append(catalog.Entries, *entry)
		}
	}
	return catalog, nil
}

// buildEntry 组装单条目录；visible 为 false 表示调用方无权看到该映射。
func (s *Service) buildEntry(actor Actor, grant Grant) (*CatalogEntry, bool, error) {
	granted, err := s.store.getVersion(grant.MappingRef, grant.Version)
	if err != nil {
		return nil, false, err
	}
	parties, err := s.versionParties(*granted)
	if err != nil {
		return nil, false, err
	}
	isParty := false
	for _, party := range parties {
		if party == actor.Institution {
			isParty = true
			break
		}
	}
	if !isParty && actor.Role != RoleLeadCenter {
		return nil, false, nil
	}
	left, err := s.store.getSubmission(granted.LeftSubmissionRef)
	if err != nil {
		return nil, false, err
	}
	right, err := s.store.getSubmission(granted.RightSubmissionRef)
	if err != nil {
		return nil, false, err
	}
	versions, err := s.store.listVersions(grant.MappingRef)
	if err != nil {
		return nil, false, err
	}
	notes, err := s.store.listNotes(grant.MappingRef, grant.Version)
	if err != nil {
		return nil, false, err
	}
	withdrawals, err := s.store.listWithdrawals(grant.MappingRef, grant.Version)
	if err != nil {
		return nil, false, err
	}
	entry := &CatalogEntry{
		IndicatorKey:   grant.IndicatorKey,
		MappingRef:     grant.MappingRef,
		GrantedVersion: grant.Version,
		LatestVersion:  versions[len(versions)-1].Version,
		Left:           *left,
		Right:          *right,
		Notes:          notes,
		IssuedAt:       grant.IssuedAt,
	}
	entry.Status = entryStatus(granted, versions, withdrawals, grant.TaskRef)
	return entry, true, nil
}

// entryStatus 按 推翻 > 撤回 > 落后 > 在用 的优先级给出条目状态。
func entryStatus(granted *Version, versions []Version, withdrawals []Withdrawal, taskRef string) string {
	if granted.Status == StatusOverturned {
		return EntryOverturned
	}
	for _, w := range withdrawals {
		if scopeCoversTask(w.Scope, taskRef) {
			return EntryWithdrawn
		}
	}
	for _, v := range versions {
		if v.Version > granted.Version && v.Status == StatusEffective {
			return EntrySuperseded
		}
	}
	return EntryActive
}

// scopeCoversTask 判断撤回范围是否覆盖指定任务。
func scopeCoversTask(scope WithdrawalScope, taskRef string) bool {
	if scope.From != "" {
		from, err := time.Parse(time.RFC3339, scope.From)
		if err != nil || time.Now().UTC().Before(from) {
			return false
		}
	}
	if len(scope.Tasks) == 0 {
		return true
	}
	for _, task := range scope.Tasks {
		if task == taskRef {
			return true
		}
	}
	return false
}

// GetMapping 返回映射及其全部版本，供当事方、牵头中心与监管查看。
func (s *Service) GetMapping(actor Actor, mappingRef string) (*Mapping, []Version, error) {
	mapping, err := s.store.getMapping(mappingRef)
	if err != nil {
		return nil, nil, notFoundIfMissing(err, "映射不存在")
	}
	versions, err := s.store.listVersions(mappingRef)
	if err != nil {
		return nil, nil, err
	}
	if actor.Role != RoleLeadCenter && actor.Role != RoleRegulator && len(versions) > 0 {
		if _, err := s.requireParty(actor, &versions[0]); err != nil {
			return nil, nil, err
		}
	}
	return mapping, versions, nil
}

// IndicatorLineage 供监管沿已发布指标追溯各机构签署与撤回范围。
func (s *Service) IndicatorLineage(actor Actor, indicatorKey string) (*Lineage, error) {
	if err := requireRole(actor, RoleRegulator, RoleLeadCenter); err != nil {
		return nil, err
	}
	mappings, err := s.store.listMappingsByIndicator(indicatorKey)
	if err != nil {
		return nil, err
	}
	if len(mappings) == 0 {
		return nil, errNotFound("指标不存在或尚未建立映射")
	}
	lineage := &Lineage{IndicatorKey: indicatorKey, Mappings: []MappingLineage{}}
	for _, mapping := range mappings {
		versions, err := s.store.listVersions(mapping.MappingRef)
		if err != nil {
			return nil, err
		}
		entry := MappingLineage{Mapping: mapping, Versions: []VersionLineage{}}
		for _, version := range versions {
			confirmations, err := s.store.listConfirmations(mapping.MappingRef, version.Version)
			if err != nil {
				return nil, err
			}
			withdrawals, err := s.store.listWithdrawals(mapping.MappingRef, version.Version)
			if err != nil {
				return nil, err
			}
			grants, err := s.store.listGrantsByVersion(mapping.MappingRef, version.Version)
			if err != nil {
				return nil, err
			}
			entry.Versions = append(entry.Versions, VersionLineage{
				Version:       version,
				Confirmations: confirmations,
				Withdrawals:   withdrawals,
				Grants:        grants,
			})
		}
		lineage.Mappings = append(lineage.Mappings, entry)
	}
	return lineage, nil
}

// notFoundIfMissing 把存储层的空结果转换为领域未找到错误。
func notFoundIfMissing(err error, message string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound("%s", message)
	}
	return err
}

// isUniqueViolation 识别 SQLite 唯一约束冲突。
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
