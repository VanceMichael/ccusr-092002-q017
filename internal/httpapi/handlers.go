package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/vancemichael/092002-medical-collaboration-control/internal/store"
)

type handlers struct {
	store *store.Store
}

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

var relationTypes = map[string]bool{
	"equivalent":   true,
	"narrower":     true,
	"broader":      true,
	"incomparable": true,
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// storeError 把持久层错误映射为 HTTP 状态码。
func storeError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "记录不存在")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "当前状态与请求冲突")
	default:
		writeError(w, http.StatusInternalServerError, "服务内部错误")
	}
	return true
}

// requireIdentity 解析身份，失败时响应 401 并返回 false。
func requireIdentity(w http.ResponseWriter, r *http.Request) (Identity, bool) {
	id, ok := identity(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "缺少身份请求头")
		return Identity{}, false
	}
	return id, true
}

// requireRole 校验请求方角色，失败时响应 403 并返回 false。
func requireRole(w http.ResponseWriter, id Identity, roles ...string) bool {
	for _, role := range roles {
		if id.Role == role {
			return true
		}
	}
	writeError(w, http.StatusForbidden, "该角色无权执行此操作")
	return false
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法的 JSON")
		return false
	}
	return true
}

func (h *handlers) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- 术语提交 ---

type createTermRequest struct {
	LocalCode          string   `json:"local_code"`
	DefinitionDigest   string   `json:"definition_digest"`
	Language           string   `json:"language"`
	Unit               string   `json:"unit"`
	AuthorizedPurposes []string `json:"authorized_purposes"`
}

func (h *handlers) createTerm(w http.ResponseWriter, r *http.Request) {
	id, ok := requireIdentity(w, r)
	if !ok {
		return
	}
	if !requireRole(w, id, RoleSubmitter, RoleClinicalLead) {
		return
	}
	var req createTermRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	switch {
	case strings.TrimSpace(req.LocalCode) == "":
		writeError(w, http.StatusBadRequest, "local_code 不能为空")
		return
	case !digestPattern.MatchString(req.DefinitionDigest):
		writeError(w, http.StatusBadRequest, "definition_digest 须为 64 位小写十六进制 SHA-256 摘要")
		return
	case strings.TrimSpace(req.Language) == "":
		writeError(w, http.StatusBadRequest, "language 不能为空")
		return
	case len(req.AuthorizedPurposes) == 0:
		writeError(w, http.StatusBadRequest, "authorized_purposes 至少一项")
		return
	}
	term := &store.TermSubmission{
		InstitutionRef:     id.InstitutionRef,
		LocalCode:          req.LocalCode,
		DefinitionDigest:   req.DefinitionDigest,
		Language:           req.Language,
		Unit:               req.Unit,
		AuthorizedPurposes: req.AuthorizedPurposes,
		SubmittedBy:        id.ActorRef,
	}
	if err := h.store.CreateTerm(r.Context(), term); storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, term)
}

func (h *handlers) getTerm(w http.ResponseWriter, r *http.Request) {
	id, ok := requireIdentity(w, r)
	if !ok {
		return
	}
	term, err := h.store.GetTerm(r.Context(), r.PathValue("termID"))
	if storeError(w, err) {
		return
	}
	if id.Role != RoleRegulator && id.InstitutionRef != term.InstitutionRef {
		// 仅提交机构、监管人员，或与该术语存在映射关系的对侧机构可见。
		visible, verr := h.termVisibleTo(r, term.TermID, id.InstitutionRef)
		if storeError(w, verr) {
			return
		}
		if !visible {
			writeError(w, http.StatusForbidden, "无权查看该术语提交")
			return
		}
	}
	writeJSON(w, http.StatusOK, term)
}

// termVisibleTo 判断该术语是否出现在请求机构参与的映射中。
func (h *handlers) termVisibleTo(r *http.Request, termID, institutionRef string) (bool, error) {
	mappings, err := h.store.ListMappingsByTerm(r.Context(), termID)
	if err != nil {
		return false, err
	}
	for _, m := range mappings {
		if m.InstitutionA == institutionRef || m.InstitutionB == institutionRef {
			return true, nil
		}
	}
	return false, nil
}

type withdrawTermRequest struct {
	WithdrawalScope string `json:"withdrawal_scope"`
}

func (h *handlers) withdrawTerm(w http.ResponseWriter, r *http.Request) {
	id, ok := requireIdentity(w, r)
	if !ok {
		return
	}
	if !requireRole(w, id, RoleSubmitter, RoleClinicalLead) {
		return
	}
	term, err := h.store.GetTerm(r.Context(), r.PathValue("termID"))
	if storeError(w, err) {
		return
	}
	if term.InstitutionRef != id.InstitutionRef {
		writeError(w, http.StatusForbidden, "仅提交机构可撤回该术语")
		return
	}
	var req withdrawTermRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.WithdrawalScope) == "" {
		writeError(w, http.StatusBadRequest, "withdrawal_scope 不能为空，须说明撤回范围")
		return
	}
	updated, err := h.store.WithdrawTerm(r.Context(), term.TermID, id.ActorRef, req.WithdrawalScope)
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// --- 映射与版本 ---

type createMappingRequest struct {
	TermA         string `json:"term_a"`
	TermB         string `json:"term_b"`
	RelationType  string `json:"relation_type"`
	AmbiguityNote string `json:"ambiguity_note"`
	ChangeNote    string `json:"change_note"`
}

func (h *handlers) createMapping(w http.ResponseWriter, r *http.Request) {
	id, ok := requireIdentity(w, r)
	if !ok {
		return
	}
	if !requireRole(w, id, RoleSubmitter, RoleClinicalLead) {
		return
	}
	var req createMappingRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !relationTypes[req.RelationType] {
		writeError(w, http.StatusBadRequest, "relation_type 须为 equivalent/narrower/broader/incomparable 之一")
		return
	}
	termA, err := h.store.GetTerm(r.Context(), req.TermA)
	if storeError(w, err) {
		return
	}
	termB, err := h.store.GetTerm(r.Context(), req.TermB)
	if storeError(w, err) {
		return
	}
	switch {
	case termA.InstitutionRef == termB.InstitutionRef:
		writeError(w, http.StatusUnprocessableEntity, "映射两侧须为不同机构")
		return
	case termA.Status != "active" || termB.Status != "active":
		writeError(w, http.StatusUnprocessableEntity, "已撤回的术语不能用于新映射")
		return
	case id.InstitutionRef != termA.InstitutionRef && id.InstitutionRef != termB.InstitutionRef:
		writeError(w, http.StatusForbidden, "仅映射参与机构可提议映射")
		return
	}
	mapping := &store.Mapping{
		TermA:        termA.TermID,
		TermB:        termB.TermID,
		InstitutionA: termA.InstitutionRef,
		InstitutionB: termB.InstitutionRef,
		CreatedBy:    id.ActorRef,
	}
	version := &store.MappingVersion{
		RelationType:  req.RelationType,
		AmbiguityNote: req.AmbiguityNote,
		ChangeNote:    req.ChangeNote,
		CreatedBy:     id.ActorRef,
	}
	if err := h.store.CreateMapping(r.Context(), mapping, version); storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"mapping": mapping, "version": version})
}

func (h *handlers) getMapping(w http.ResponseWriter, r *http.Request) {
	id, ok := requireIdentity(w, r)
	if !ok {
		return
	}
	mapping, err := h.store.GetMapping(r.Context(), r.PathValue("mappingID"))
	if storeError(w, err) {
		return
	}
	if id.Role != RoleRegulator && id.InstitutionRef != mapping.InstitutionA && id.InstitutionRef != mapping.InstitutionB {
		writeError(w, http.StatusForbidden, "仅映射参与机构或监管人员可查看")
		return
	}
	versions, err := h.store.ListVersions(r.Context(), mapping.MappingID)
	if storeError(w, err) {
		return
	}
	type versionView struct {
		store.MappingVersion
		Confirmations []store.Confirmation `json:"confirmations"`
	}
	views := make([]versionView, 0, len(versions))
	for _, v := range versions {
		confirmations, err := h.store.ListConfirmations(r.Context(), mapping.MappingID, v.VersionNo)
		if storeError(w, err) {
			return
		}
		if confirmations == nil {
			confirmations = []store.Confirmation{}
		}
		views = append(views, versionView{MappingVersion: v, Confirmations: confirmations})
	}
	writeJSON(w, http.StatusOK, map[string]any{"mapping": mapping, "versions": views})
}

type createVersionRequest struct {
	RelationType  string `json:"relation_type"`
	AmbiguityNote string `json:"ambiguity_note"`
	ChangeNote    string `json:"change_note"`
}

func (h *handlers) createVersion(w http.ResponseWriter, r *http.Request) {
	id, ok := requireIdentity(w, r)
	if !ok {
		return
	}
	if !requireRole(w, id, RoleSubmitter, RoleClinicalLead) {
		return
	}
	mapping, err := h.store.GetMapping(r.Context(), r.PathValue("mappingID"))
	if storeError(w, err) {
		return
	}
	if id.InstitutionRef != mapping.InstitutionA && id.InstitutionRef != mapping.InstitutionB {
		writeError(w, http.StatusForbidden, "仅映射参与机构可提议新版本")
		return
	}
	var req createVersionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !relationTypes[req.RelationType] {
		writeError(w, http.StatusBadRequest, "relation_type 须为 equivalent/narrower/broader/incomparable 之一")
		return
	}
	if strings.TrimSpace(req.ChangeNote) == "" {
		writeError(w, http.StatusBadRequest, "change_note 不能为空，须说明修订原因")
		return
	}
	version := &store.MappingVersion{
		MappingID:     mapping.MappingID,
		RelationType:  req.RelationType,
		AmbiguityNote: req.AmbiguityNote,
		ChangeNote:    req.ChangeNote,
		CreatedBy:     id.ActorRef,
	}
	if err := h.store.CreateVersion(r.Context(), version); storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, version)
}

type signVersionRequest struct {
	Note string `json:"note"`
}

func (h *handlers) confirmVersion(w http.ResponseWriter, r *http.Request) {
	h.signVersion(w, r, "confirm")
}

func (h *handlers) rejectVersion(w http.ResponseWriter, r *http.Request) {
	h.signVersion(w, r, "reject")
}

// signVersion 处理临床负责人对映射版本的确认或否决。
// 确认在双边齐全后使版本生效；否决立即使版本失效。签署记录只增不改。
func (h *handlers) signVersion(w http.ResponseWriter, r *http.Request, decision string) {
	id, ok := requireIdentity(w, r)
	if !ok {
		return
	}
	if !requireRole(w, id, RoleClinicalLead) {
		return
	}
	mapping, err := h.store.GetMapping(r.Context(), r.PathValue("mappingID"))
	if storeError(w, err) {
		return
	}
	if id.InstitutionRef != mapping.InstitutionA && id.InstitutionRef != mapping.InstitutionB {
		writeError(w, http.StatusForbidden, "仅映射两侧机构的临床负责人可签署")
		return
	}
	versionNo, err := strconv.Atoi(r.PathValue("versionNo"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "versionNo 须为整数")
		return
	}
	version, err := h.store.GetVersion(r.Context(), mapping.MappingID, versionNo)
	if storeError(w, err) {
		return
	}
	if version.Status != "pending" {
		writeError(w, http.StatusConflict, "该版本不在待确认状态")
		return
	}
	var req signVersionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	confirmation := &store.Confirmation{
		MappingID:      mapping.MappingID,
		VersionNo:      versionNo,
		InstitutionRef: id.InstitutionRef,
		Decision:       decision,
		SignedBy:       id.ActorRef,
		Note:           req.Note,
	}
	if err := h.store.AddConfirmation(r.Context(), confirmation); storeError(w, err) {
		return
	}
	if decision == "reject" {
		if err := h.store.RejectVersion(r.Context(), mapping.MappingID, versionNo); storeError(w, err) {
			return
		}
	} else {
		// 双边均确认后版本生效，旧生效版本自动转为历史版本。
		confirmations, err := h.store.ListConfirmations(r.Context(), mapping.MappingID, versionNo)
		if storeError(w, err) {
			return
		}
		confirmed := map[string]bool{}
		for _, c := range confirmations {
			if c.Decision == "confirm" {
				confirmed[c.InstitutionRef] = true
			}
		}
		if confirmed[mapping.InstitutionA] && confirmed[mapping.InstitutionB] {
			if err := h.store.ActivateVersion(r.Context(), mapping.MappingID, versionNo); storeError(w, err) {
				return
			}
		}
	}
	updated, err := h.store.GetVersion(r.Context(), mapping.MappingID, versionNo)
	if storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// --- 指标目录 ---

type createCatalogRequest struct {
	TaskRef string `json:"task_ref"`
	Entries []struct {
		IndicatorKey string `json:"indicator_key"`
		MappingID    string `json:"mapping_id"`
	} `json:"entries"`
}

func (h *handlers) createCatalog(w http.ResponseWriter, r *http.Request) {
	id, ok := requireIdentity(w, r)
	if !ok {
		return
	}
	if !requireRole(w, id, RoleClinicalLead) {
		return
	}
	var req createCatalogRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.TaskRef) == "" {
		writeError(w, http.StatusBadRequest, "task_ref 不能为空")
		return
	}
	if len(req.Entries) == 0 {
		writeError(w, http.StatusBadRequest, "entries 至少一条")
		return
	}
	seen := map[string]bool{}
	entries := make([]store.CatalogEntry, 0, len(req.Entries))
	for _, e := range req.Entries {
		if strings.TrimSpace(e.IndicatorKey) == "" {
			writeError(w, http.StatusBadRequest, "indicator_key 不能为空")
			return
		}
		if seen[e.IndicatorKey] {
			writeError(w, http.StatusBadRequest, "indicator_key 在目录内重复")
			return
		}
		seen[e.IndicatorKey] = true
		mapping, err := h.store.GetMapping(r.Context(), e.MappingID)
		if storeError(w, err) {
			return
		}
		// 只有双边确认生效的版本才能进入指标目录。
		if mapping.CurrentVersion == nil {
			writeError(w, http.StatusUnprocessableEntity, "映射 "+e.MappingID+" 尚无生效版本，不能发放")
			return
		}
		entries = append(entries, store.CatalogEntry{
			IndicatorKey: e.IndicatorKey,
			MappingID:    mapping.MappingID,
			VersionNo:    *mapping.CurrentVersion,
		})
	}
	catalog := &store.Catalog{TaskRef: req.TaskRef, IssuedBy: id.InstitutionRef}
	if err := h.store.CreateCatalog(r.Context(), catalog, entries); storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"catalog": catalog, "entries": entries})
}

func (h *handlers) getCatalog(w http.ResponseWriter, r *http.Request) {
	id, ok := requireIdentity(w, r)
	if !ok {
		return
	}
	catalog, entries, err := h.store.GetCatalog(r.Context(), r.PathValue("catalogID"))
	if storeError(w, err) {
		return
	}
	if id.Role != RoleRegulator && id.InstitutionRef != catalog.IssuedBy {
		involved, verr := h.catalogInvolves(r, entries, id.InstitutionRef)
		if storeError(w, verr) {
			return
		}
		if !involved {
			writeError(w, http.StatusForbidden, "无权查看该目录")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"catalog": catalog, "entries": entries})
}

// catalogInvolves 判断请求机构是否为目录任一条目映射的参与方。
func (h *handlers) catalogInvolves(r *http.Request, entries []store.CatalogEntry, institutionRef string) (bool, error) {
	for _, e := range entries {
		mapping, err := h.store.GetMapping(r.Context(), e.MappingID)
		if err != nil {
			return false, err
		}
		if mapping.InstitutionA == institutionRef || mapping.InstitutionB == institutionRef {
			return true, nil
		}
	}
	return false, nil
}

// getTaskCatalog 是合作方查询入口：仅返回该机构参与的协作任务下
// 获授权的映射与歧义说明，并标注绑定版本是否已被新版本取代。
func (h *handlers) getTaskCatalog(w http.ResponseWriter, r *http.Request) {
	id, ok := requireIdentity(w, r)
	if !ok {
		return
	}
	taskRef := r.PathValue("taskRef")
	catalogs, err := h.store.ListCatalogsByTask(r.Context(), taskRef)
	if storeError(w, err) {
		return
	}
	if len(catalogs) == 0 {
		writeError(w, http.StatusNotFound, "该协作任务暂无指标目录")
		return
	}
	type entryView struct {
		IndicatorRef   string                 `json:"indicator_ref"`
		IndicatorKey   string                 `json:"indicator_key"`
		MappingID      string                 `json:"mapping_id"`
		BoundVersion   int                    `json:"bound_version"`
		VersionStatus  string                 `json:"version_status"`
		CurrentVersion *int                   `json:"current_version"`
		RelationType   string                 `json:"relation_type"`
		AmbiguityNote  string                 `json:"ambiguity_note"`
		Terms          []store.TermSubmission `json:"terms"`
		Lineage        string                 `json:"lineage"`
	}
	type catalogView struct {
		CatalogID string      `json:"catalog_id"`
		IssuedBy  string      `json:"issued_by"`
		IssuedAt  string      `json:"issued_at"`
		Entries   []entryView `json:"entries"`
	}
	views := make([]catalogView, 0, len(catalogs))
	authorized := false
	for _, c := range catalogs {
		if c.IssuedBy == id.InstitutionRef {
			authorized = true
		}
		entries, err := h.store.ListEntries(r.Context(), c.CatalogID)
		if storeError(w, err) {
			return
		}
		view := catalogView{CatalogID: c.CatalogID, IssuedBy: c.IssuedBy, IssuedAt: c.IssuedAt, Entries: []entryView{}}
		for _, e := range entries {
			mapping, err := h.store.GetMapping(r.Context(), e.MappingID)
			if storeError(w, err) {
				return
			}
			if mapping.InstitutionA == id.InstitutionRef || mapping.InstitutionB == id.InstitutionRef {
				authorized = true
			}
			version, err := h.store.GetVersion(r.Context(), e.MappingID, e.VersionNo)
			if storeError(w, err) {
				return
			}
			termA, err := h.store.GetTerm(r.Context(), mapping.TermA)
			if storeError(w, err) {
				return
			}
			termB, err := h.store.GetTerm(r.Context(), mapping.TermB)
			if storeError(w, err) {
				return
			}
			view.Entries = append(view.Entries, entryView{
				IndicatorRef:   e.IndicatorRef,
				IndicatorKey:   e.IndicatorKey,
				MappingID:      e.MappingID,
				BoundVersion:   e.VersionNo,
				VersionStatus:  version.Status,
				CurrentVersion: mapping.CurrentVersion,
				RelationType:   version.RelationType,
				AmbiguityNote:  version.AmbiguityNote,
				Terms:          []store.TermSubmission{*termA, *termB},
				Lineage:        "/catalogs/" + c.CatalogID + "/entries/" + e.IndicatorKey + "/lineage",
			})
		}
		views = append(views, view)
	}
	if id.Role != RoleRegulator && !authorized {
		writeError(w, http.StatusForbidden, "该机构未参与此协作任务，无权查询")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task_ref": taskRef, "catalogs": views})
}

// getIndicatorLineage 供监管人员沿已发布指标追溯：
// 目录条目 → 绑定映射版本 → 参与机构签署 → 术语撤回范围，以及该版本是否已被取代。
func (h *handlers) getIndicatorLineage(w http.ResponseWriter, r *http.Request) {
	id, ok := requireIdentity(w, r)
	if !ok {
		return
	}
	if !requireRole(w, id, RoleRegulator) {
		return
	}
	indicatorRef := r.PathValue("catalogID") + "/" + r.PathValue("indicatorKey")
	entry, err := h.store.GetEntryByIndicatorRef(r.Context(), indicatorRef)
	if storeError(w, err) {
		return
	}
	catalog, _, err := h.store.GetCatalog(r.Context(), entry.CatalogID)
	if storeError(w, err) {
		return
	}
	mapping, err := h.store.GetMapping(r.Context(), entry.MappingID)
	if storeError(w, err) {
		return
	}
	version, err := h.store.GetVersion(r.Context(), entry.MappingID, entry.VersionNo)
	if storeError(w, err) {
		return
	}
	confirmations, err := h.store.ListConfirmations(r.Context(), entry.MappingID, entry.VersionNo)
	if storeError(w, err) {
		return
	}
	if confirmations == nil {
		confirmations = []store.Confirmation{}
	}
	termA, err := h.store.GetTerm(r.Context(), mapping.TermA)
	if storeError(w, err) {
		return
	}
	termB, err := h.store.GetTerm(r.Context(), mapping.TermB)
	if storeError(w, err) {
		return
	}
	// 绑定版本已被取代时，指出当前生效版本；历史版本与签署保持可查。
	var supersededBy *int
	if version.Status == "superseded" {
		supersededBy = mapping.CurrentVersion
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"indicator_ref": indicatorRef,
		"catalog":       catalog,
		"mapping":       mapping,
		"bound_version": version,
		"superseded_by": supersededBy,
		"confirmations": confirmations,
		"terms":         []store.TermSubmission{*termA, *termB},
	})
}
