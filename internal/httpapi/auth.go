package httpapi

import "net/http"

// 参与方角色。骨架阶段通过请求头声明，生产环境应替换为可验证的凭证。
const (
	RoleSubmitter    = "submitter"     // 机构提交人：提交术语、提议映射
	RoleClinicalLead = "clinical_lead" // 临床负责人：确认映射版本、发放目录
	RolePartner      = "partner"       // 合作方：查询获授权的映射与歧义说明
	RoleRegulator    = "regulator"     // 监管人员：追溯签署与撤回范围
)

// Identity 是请求方声明的身份。
type Identity struct {
	InstitutionRef string
	ActorRef       string
	Role           string
}

// identity 从请求头解析身份；任一字段缺失即视为未认证。
func identity(r *http.Request) (Identity, bool) {
	id := Identity{
		InstitutionRef: r.Header.Get("X-Institution-Ref"),
		ActorRef:       r.Header.Get("X-Actor-Ref"),
		Role:           r.Header.Get("X-Actor-Role"),
	}
	if id.InstitutionRef == "" || id.ActorRef == "" || id.Role == "" {
		return Identity{}, false
	}
	return id, true
}
