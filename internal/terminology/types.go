// Package terminology 实现跨机构术语与用途协议的领域逻辑。
//
// 中心只保存本地字段定义的不可逆摘要及授权元数据，不接收原始病历，
// 也不执行模型训练。除映射版本的状态推进（pending→effective→overturned）
// 之外，所有记录只增不删：术语翻译被推翻或机构撤回时，历史版本、
// 签署与已发放的目录全部保留，供监管沿指标溯源。
package terminology

import "time"

// 调用方角色，由请求头携带，服务内只做结构性校验。
const (
	RoleDataSteward  = "data-steward"  // 机构数据管理员：提交字段摘要、发起映射、写歧义说明
	RoleClinicalLead = "clinical-lead" // 临床负责人：确认版本、推翻翻译、撤回授权
	RoleLeadCenter   = "lead-center"   // 牵头医院协调方：向协作任务发放指标目录
	RoleRegulator    = "regulator"     // 监管人员：只读溯源
)

// Actor 是一次请求的调用方身份。
type Actor struct {
	Institution string
	Role        string
}

// 映射版本状态。
const (
	StatusPending    = "pending"
	StatusEffective  = "effective"
	StatusOverturned = "overturned"
)

// Submission 是机构提交的本地字段定义摘要，中心不保存定义原文。
type Submission struct {
	SubmissionRef    string   `json:"submission_ref"`
	InstitutionRef   string   `json:"institution_ref"`
	LocalFieldRef    string   `json:"local_field_ref"`
	DefinitionDigest string   `json:"definition_digest"`
	Language         string   `json:"language"`
	Unit             string   `json:"unit"`
	Purposes         []string `json:"purposes"`
	SubmittedAt      string   `json:"submitted_at"`
}

// Mapping 把一个共享指标键关联到一组版本化的映射关系。
type Mapping struct {
	MappingRef   string `json:"mapping_ref"`
	IndicatorKey string `json:"indicator_key"`
	CreatedAt    string `json:"created_at"`
}

// Version 是映射关系的一个版本；同一映射下版本单调递增、不可改写。
type Version struct {
	MappingRef         string `json:"mapping_ref"`
	Version            int    `json:"version"`
	LeftSubmissionRef  string `json:"left_submission_ref"`
	RightSubmissionRef string `json:"right_submission_ref"`
	EquivalenceNote    string `json:"equivalence_note"`
	Status             string `json:"status"`
	StatusReason       string `json:"status_reason,omitempty"`
	CreatedAt          string `json:"created_at"`
}

// Confirmation 是一方临床负责人对某个映射版本的签署。
type Confirmation struct {
	ConfirmationRef string `json:"confirmation_ref"`
	MappingRef      string `json:"mapping_ref"`
	Version         int    `json:"version"`
	InstitutionRef  string `json:"institution_ref"`
	SignatoryRef    string `json:"signatory_ref"`
	SignedAt        string `json:"signed_at"`
}

// WithdrawalScope 描述撤回覆盖的范围；空切片表示不限制该维度。
type WithdrawalScope struct {
	Tasks    []string `json:"tasks,omitempty"`
	Purposes []string `json:"purposes,omitempty"`
	From     string   `json:"from,omitempty"`
}

// Withdrawal 是机构对某版本的撤回记录，只追加、不删除既有签署。
type Withdrawal struct {
	WithdrawalRef  string          `json:"withdrawal_ref"`
	MappingRef     string          `json:"mapping_ref"`
	Version        int             `json:"version"`
	InstitutionRef string          `json:"institution_ref"`
	Scope          WithdrawalScope `json:"scope"`
	Reason         string          `json:"reason"`
	WithdrawnAt    string          `json:"withdrawn_at"`
}

// Grant 是一次向指定协作任务发放指标目录的记录。
type Grant struct {
	GrantRef     string `json:"grant_ref"`
	TaskRef      string `json:"task_ref"`
	IndicatorKey string `json:"indicator_key"`
	MappingRef   string `json:"mapping_ref"`
	Version      int    `json:"version"`
	IssuedAt     string `json:"issued_at"`
}

// AmbiguityNote 是挂在映射版本上的歧义说明。
type AmbiguityNote struct {
	NoteRef              string `json:"note_ref"`
	MappingRef           string `json:"mapping_ref"`
	Version              int    `json:"version"`
	AuthorInstitutionRef string `json:"author_institution_ref"`
	Body                 string `json:"body"`
	CreatedAt            string `json:"created_at"`
}

// 目录条目状态。
const (
	EntryActive     = "active"     // 发放版本即当前生效版本
	EntrySuperseded = "superseded" // 已有更新的生效版本，发放版本落后
	EntryOverturned = "overturned" // 发放版本的翻译已被推翻
	EntryWithdrawn  = "withdrawn"  // 存在覆盖该任务的撤回记录
)

// CatalogEntry 是合作方看到的单条目录内容。
type CatalogEntry struct {
	IndicatorKey   string          `json:"indicator_key"`
	MappingRef     string          `json:"mapping_ref"`
	GrantedVersion int             `json:"granted_version"`
	LatestVersion  int             `json:"latest_version"`
	Status         string          `json:"status"`
	Left           Submission      `json:"left"`
	Right          Submission      `json:"right"`
	Notes          []AmbiguityNote `json:"ambiguity_notes"`
	IssuedAt       string          `json:"issued_at"`
}

// Catalog 是指定任务下、按调用方授权范围过滤后的指标目录。
type Catalog struct {
	TaskRef string         `json:"task_ref"`
	Entries []CatalogEntry `json:"entries"`
}

// VersionLineage 是监管视角下一个版本的完整履历。
type VersionLineage struct {
	Version       Version        `json:"version"`
	Confirmations []Confirmation `json:"confirmations"`
	Withdrawals   []Withdrawal   `json:"withdrawals"`
	Grants        []Grant        `json:"grants"`
}

// MappingLineage 按映射分组呈现版本履历。
type MappingLineage struct {
	Mapping  Mapping          `json:"mapping"`
	Versions []VersionLineage `json:"versions"`
}

// Lineage 是一个已发布指标从签署到撤回的完整溯源。
type Lineage struct {
	IndicatorKey string           `json:"indicator_key"`
	Mappings     []MappingLineage `json:"mappings"`
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}
