package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// 测试用的两家机构与监管人员身份。
var (
	institutionA = Identity{InstitutionRef: "HOSPITAL-A", ActorRef: "dr-a", Role: RoleClinicalLead}
	institutionB = Identity{InstitutionRef: "CLINIC-B", ActorRef: "dr-b", Role: RoleClinicalLead}
	submitterA   = Identity{InstitutionRef: "HOSPITAL-A", ActorRef: "staff-a", Role: RoleSubmitter}
	submitterB   = Identity{InstitutionRef: "CLINIC-B", ActorRef: "staff-b", Role: RoleSubmitter}
	partnerB     = Identity{InstitutionRef: "CLINIC-B", ActorRef: "analyst-b", Role: RolePartner}
	outsider     = Identity{InstitutionRef: "ORG-C", ActorRef: "staff-c", Role: RolePartner}
	regulator    = Identity{InstitutionRef: "REGULATOR", ActorRef: "audit-1", Role: RoleRegulator}
)

func digestOf(seed string) string {
	// 测试用的不可逆摘要：64 位小写十六进制。
	return strings.Repeat(seed, 64/len(seed))
}

// submitTerm 提交一条术语并返回 term_id。
func submitTerm(t *testing.T, handler http.Handler, id Identity, localCode, language, unit string) string {
	t.Helper()
	response := call(t, handler, http.MethodPost, "/terms", map[string]any{
		"local_code":          localCode,
		"definition_digest":   digestOf("ab"),
		"language":            language,
		"unit":                unit,
		"authorized_purposes": []string{"primary-care-comparison"},
	}, id)
	if response.Code != http.StatusCreated {
		t.Fatalf("提交术语失败，状态码 %d，响应 %s", response.Code, response.Body.String())
	}
	return decode(t, response)["term_id"].(string)
}

// proposeMapping 提议映射并返回 mapping_id。
func proposeMapping(t *testing.T, handler http.Handler, id Identity, termA, termB string) string {
	t.Helper()
	response := call(t, handler, http.MethodPost, "/mappings", map[string]any{
		"term_a":         termA,
		"term_b":         termB,
		"relation_type":  "equivalent",
		"ambiguity_note": "A 方按千住院日计，B 方按千门诊人次计，分母口径不同",
	}, id)
	if response.Code != http.StatusCreated {
		t.Fatalf("提议映射失败，状态码 %d，响应 %s", response.Code, response.Body.String())
	}
	return decode(t, response)["mapping"].(map[string]any)["mapping_id"].(string)
}

// confirm 由指定身份确认映射版本，返回响应状态码。
func confirm(t *testing.T, handler http.Handler, id Identity, mappingID string, version int) int {
	t.Helper()
	return call(t, handler, http.MethodPost,
		fmt.Sprintf("/mappings/%s/versions/%d/confirm", mappingID, version),
		map[string]any{"note": "临床负责人已核对"}, id).Code
}

// setupEffectiveMapping 建立一对术语并完成双边确认，返回 (mappingID, termA, termB)。
func setupEffectiveMapping(t *testing.T, handler http.Handler) (string, string, string) {
	t.Helper()
	termA := submitTerm(t, handler, submitterA, "FALL-RATE", "zh-CN", "per_1000_patient_days")
	termB := submitTerm(t, handler, submitterB, "fall_rate", "en", "per_1000_visits")
	mappingID := proposeMapping(t, handler, submitterA, termA, termB)
	if code := confirm(t, handler, institutionA, mappingID, 1); code != http.StatusOK {
		t.Fatalf("A 方确认失败，状态码 %d", code)
	}
	if code := confirm(t, handler, institutionB, mappingID, 1); code != http.StatusOK {
		t.Fatalf("B 方确认失败，状态码 %d", code)
	}
	return mappingID, termA, termB
}

// issueCatalog 发放目录并返回 (catalog_id, indicator_ref)。
func issueCatalog(t *testing.T, handler http.Handler, id Identity, taskRef, mappingID string) (string, string) {
	t.Helper()
	response := call(t, handler, http.MethodPost, "/catalogs", map[string]any{
		"task_ref": taskRef,
		"entries":  []map[string]any{{"indicator_key": "IND-FALL-RATE", "mapping_id": mappingID}},
	}, id)
	if response.Code != http.StatusCreated {
		t.Fatalf("发放目录失败，状态码 %d，响应 %s", response.Code, response.Body.String())
	}
	body := decode(t, response)
	entries := body["entries"].([]any)
	return body["catalog"].(map[string]any)["catalog_id"].(string),
		entries[0].(map[string]any)["indicator_ref"].(string)
}

func TestTermSubmissionValidation(t *testing.T) {
	handler := newTestHandler(t)

	// 缺少身份头。
	if response := call(t, handler, http.MethodPost, "/terms", map[string]any{}, Identity{}); response.Code != http.StatusUnauthorized {
		t.Fatalf("缺少身份头应返回 401，实际 %d", response.Code)
	}
	// 摘要不是 64 位十六进制。
	response := call(t, handler, http.MethodPost, "/terms", map[string]any{
		"local_code":          "FALL-RATE",
		"definition_digest":   "not-a-digest",
		"language":            "zh-CN",
		"authorized_purposes": []string{"primary-care-comparison"},
	}, submitterA)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("非法摘要应返回 400，实际 %d", response.Code)
	}
	// 缺少授权用途。
	response = call(t, handler, http.MethodPost, "/terms", map[string]any{
		"local_code":        "FALL-RATE",
		"definition_digest": digestOf("ab"),
		"language":          "zh-CN",
	}, submitterA)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("缺少授权用途应返回 400，实际 %d", response.Code)
	}
	// 监管角色不能提交术语。
	response = call(t, handler, http.MethodPost, "/terms", map[string]any{
		"local_code":          "FALL-RATE",
		"definition_digest":   digestOf("ab"),
		"language":            "zh-CN",
		"authorized_purposes": []string{"primary-care-comparison"},
	}, regulator)
	if response.Code != http.StatusForbidden {
		t.Fatalf("监管角色提交术语应返回 403，实际 %d", response.Code)
	}
}

func TestDualConfirmationActivatesVersion(t *testing.T) {
	handler := newTestHandler(t)
	termA := submitTerm(t, handler, submitterA, "FALL-RATE", "zh-CN", "per_1000_patient_days")
	termB := submitTerm(t, handler, submitterB, "fall_rate", "en", "per_1000_visits")
	mappingID := proposeMapping(t, handler, submitterA, termA, termB)

	// 仅 A 方确认：版本仍未生效，不能发放目录。
	if code := confirm(t, handler, institutionA, mappingID, 1); code != http.StatusOK {
		t.Fatalf("A 方确认失败，状态码 %d", code)
	}
	response := call(t, handler, http.MethodPost, "/catalogs", map[string]any{
		"task_ref": "TASK-PC-2026",
		"entries":  []map[string]any{{"indicator_key": "IND-FALL-RATE", "mapping_id": mappingID}},
	}, institutionA)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("未生效映射发放目录应返回 422，实际 %d", response.Code)
	}
	// 同一机构重复确认。
	if code := confirm(t, handler, institutionA, mappingID, 1); code != http.StatusConflict {
		t.Fatalf("重复确认应返回 409，实际 %d", code)
	}
	// B 方确认后版本生效。
	if code := confirm(t, handler, institutionB, mappingID, 1); code != http.StatusOK {
		t.Fatalf("B 方确认失败，状态码 %d", code)
	}
	response = call(t, handler, http.MethodGet, "/mappings/"+mappingID, nil, institutionA)
	mapping := decode(t, response)["mapping"].(map[string]any)
	if mapping["current_version"].(float64) != 1 {
		t.Fatalf("双边确认后当前版本应为 1，实际 %v", mapping["current_version"])
	}
	// 已生效版本不能再签署。
	if code := confirm(t, handler, institutionA, mappingID, 1); code != http.StatusConflict {
		t.Fatalf("已生效版本再签署应返回 409，实际 %d", code)
	}
}

func TestCatalogQueryAndLineage(t *testing.T) {
	handler := newTestHandler(t)
	mappingID, _, _ := setupEffectiveMapping(t, handler)
	catalogID, _ := issueCatalog(t, handler, institutionA, "TASK-PC-2026", mappingID)

	// 合作方查询：返回获授权的映射与歧义说明。
	response := call(t, handler, http.MethodGet, "/tasks/TASK-PC-2026/catalog", nil, partnerB)
	if response.Code != http.StatusOK {
		t.Fatalf("合作方查询应返回 200，实际 %d，响应 %s", response.Code, response.Body.String())
	}
	catalogs := decode(t, response)["catalogs"].([]any)
	entries := catalogs[0].(map[string]any)["entries"].([]any)
	entry := entries[0].(map[string]any)
	if entry["ambiguity_note"] == "" {
		t.Fatal("查询结果应包含歧义说明")
	}
	if entry["version_status"] != "effective" {
		t.Fatalf("绑定版本状态应为 effective，实际 %v", entry["version_status"])
	}
	// 无关机构查询被拒绝。
	response = call(t, handler, http.MethodGet, "/tasks/TASK-PC-2026/catalog", nil, outsider)
	if response.Code != http.StatusForbidden {
		t.Fatalf("无关机构查询应返回 403，实际 %d", response.Code)
	}
	// 监管人员沿已发布指标追溯签署。
	response = call(t, handler, http.MethodGet,
		"/catalogs/"+catalogID+"/entries/IND-FALL-RATE/lineage", nil, regulator)
	if response.Code != http.StatusOK {
		t.Fatalf("监管追溯应返回 200，实际 %d，响应 %s", response.Code, response.Body.String())
	}
	lineage := decode(t, response)
	confirmations := lineage["confirmations"].([]any)
	if len(confirmations) != 2 {
		t.Fatalf("应有两条机构签署记录，实际 %d", len(confirmations))
	}
	signed := map[string]bool{}
	for _, c := range confirmations {
		signed[c.(map[string]any)["institution_ref"].(string)] = true
	}
	if !signed["HOSPITAL-A"] || !signed["CLINIC-B"] {
		t.Fatalf("签署记录应覆盖两家机构，实际 %v", signed)
	}
	// 非监管角色不能追溯。
	response = call(t, handler, http.MethodGet,
		"/catalogs/"+catalogID+"/entries/IND-FALL-RATE/lineage", nil, partnerB)
	if response.Code != http.StatusForbidden {
		t.Fatalf("非监管角色追溯应返回 403，实际 %d", response.Code)
	}
}

func TestSupersededVersionKeepsHistory(t *testing.T) {
	handler := newTestHandler(t)
	mappingID, _, _ := setupEffectiveMapping(t, handler)
	catalogID, _ := issueCatalog(t, handler, institutionA, "TASK-PC-2026", mappingID)

	// 翻译被推翻：提议新版本，双边确认后生效。
	response := call(t, handler, http.MethodPost, "/mappings/"+mappingID+"/versions", map[string]any{
		"relation_type":  "narrower",
		"ambiguity_note": "修订：B 方 fall_rate 仅含住院患者，口径窄于 A 方",
		"change_note":    "越文译法被临床否决，改按住院日口径",
	}, submitterB)
	if response.Code != http.StatusCreated {
		t.Fatalf("提议新版本失败，状态码 %d，响应 %s", response.Code, response.Body.String())
	}
	if code := confirm(t, handler, institutionA, mappingID, 2); code != http.StatusOK {
		t.Fatalf("A 方确认 v2 失败，状态码 %d", code)
	}
	if code := confirm(t, handler, institutionB, mappingID, 2); code != http.StatusOK {
		t.Fatalf("B 方确认 v2 失败，状态码 %d", code)
	}

	// 旧版本保留为 superseded，签署记录完整可查。
	response = call(t, handler, http.MethodGet, "/mappings/"+mappingID, nil, institutionA)
	versions := decode(t, response)["versions"].([]any)
	if len(versions) != 2 {
		t.Fatalf("应保留两个版本，实际 %d", len(versions))
	}
	v1 := versions[0].(map[string]any)
	if v1["status"] != "superseded" {
		t.Fatalf("v1 应为 superseded，实际 %v", v1["status"])
	}
	if len(v1["confirmations"].([]any)) != 2 {
		t.Fatal("v1 的历史签署记录必须保留")
	}
	// 目录条目仍绑定发放时的 v1，查询时标注已被取代。
	response = call(t, handler, http.MethodGet, "/tasks/TASK-PC-2026/catalog", nil, partnerB)
	entry := decode(t, response)["catalogs"].([]any)[0].(map[string]any)["entries"].([]any)[0].(map[string]any)
	if entry["bound_version"].(float64) != 1 {
		t.Fatalf("目录条目应仍绑定 v1，实际 %v", entry["bound_version"])
	}
	if entry["version_status"] != "superseded" {
		t.Fatalf("绑定版本状态应为 superseded，实际 %v", entry["version_status"])
	}
	if entry["current_version"].(float64) != 2 {
		t.Fatalf("当前生效版本应为 2，实际 %v", entry["current_version"])
	}
	// 监管沿旧指标追溯，可见取代关系。
	response = call(t, handler, http.MethodGet,
		"/catalogs/"+catalogID+"/entries/IND-FALL-RATE/lineage", nil, regulator)
	lineage := decode(t, response)
	if lineage["superseded_by"].(float64) != 2 {
		t.Fatalf("追溯应显示被 v2 取代，实际 %v", lineage["superseded_by"])
	}
}

func TestWithdrawalScopePreserved(t *testing.T) {
	handler := newTestHandler(t)
	mappingID, _, termB := setupEffectiveMapping(t, handler)
	catalogID, _ := issueCatalog(t, handler, institutionA, "TASK-PC-2026", mappingID)

	// B 方撤回术语，必须说明范围。
	response := call(t, handler, http.MethodPost, "/terms/"+termB+"/withdraw",
		map[string]any{"withdrawal_scope": "自 2026-10-01 起不再授权用于新协作任务"}, institutionB)
	if response.Code != http.StatusOK {
		t.Fatalf("撤回失败，状态码 %d，响应 %s", response.Code, response.Body.String())
	}
	// 重复撤回。
	response = call(t, handler, http.MethodPost, "/terms/"+termB+"/withdraw",
		map[string]any{"withdrawal_scope": "再次撤回"}, institutionB)
	if response.Code != http.StatusConflict {
		t.Fatalf("重复撤回应返回 409，实际 %d", response.Code)
	}
	// 已撤回的术语不能用于新映射。
	termC := submitTerm(t, handler, submitterA, "FALL-RATE-V2", "zh-CN", "per_1000_patient_days")
	response = call(t, handler, http.MethodPost, "/mappings", map[string]any{
		"term_a": termC, "term_b": termB, "relation_type": "equivalent",
	}, submitterA)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("已撤回术语提议新映射应返回 422，实际 %d", response.Code)
	}
	// 监管追溯可见撤回范围，历史目录与签署不受影响。
	response = call(t, handler, http.MethodGet,
		"/catalogs/"+catalogID+"/entries/IND-FALL-RATE/lineage", nil, regulator)
	terms := decode(t, response)["terms"].([]any)
	var withdrawn map[string]any
	for _, term := range terms {
		candidate := term.(map[string]any)
		if candidate["status"] == "withdrawn" {
			withdrawn = candidate
		}
	}
	if withdrawn == nil {
		t.Fatal("追溯结果应包含已撤回的术语")
	}
	if withdrawn["withdrawal_scope"] == "" {
		t.Fatal("追溯结果应保留撤回范围说明")
	}
	// 他方机构不能替 B 方撤回。
	termD := submitTerm(t, handler, submitterB, "bed-days", "en", "days")
	response = call(t, handler, http.MethodPost, "/terms/"+termD+"/withdraw",
		map[string]any{"withdrawal_scope": "越权撤回"}, institutionA)
	if response.Code != http.StatusForbidden {
		t.Fatalf("越权撤回应返回 403，实际 %d", response.Code)
	}
}

func TestPendingVersionBlocksNewProposal(t *testing.T) {
	handler := newTestHandler(t)
	termA := submitTerm(t, handler, submitterA, "FALL-RATE", "zh-CN", "per_1000_patient_days")
	termB := submitTerm(t, handler, submitterB, "fall_rate", "en", "per_1000_visits")
	mappingID := proposeMapping(t, handler, submitterA, termA, termB)

	// 存在待确认版本时不能再提议新版本。
	response := call(t, handler, http.MethodPost, "/mappings/"+mappingID+"/versions", map[string]any{
		"relation_type": "narrower", "change_note": "重复修订",
	}, submitterA)
	if response.Code != http.StatusConflict {
		t.Fatalf("存在待确认版本时提议新版本应返回 409，实际 %d", response.Code)
	}
	// 否决后可以重新提议。
	response = call(t, handler, http.MethodPost, "/mappings/"+mappingID+"/versions/1/reject",
		map[string]any{"note": "分母口径不可比"}, institutionB)
	if response.Code != http.StatusOK {
		t.Fatalf("否决失败，状态码 %d", response.Code)
	}
	response = call(t, handler, http.MethodPost, "/mappings/"+mappingID+"/versions", map[string]any{
		"relation_type": "incomparable", "change_note": "按不可比重新约定",
	}, submitterA)
	if response.Code != http.StatusCreated {
		t.Fatalf("否决后提议新版本应返回 201，实际 %d，响应 %s", response.Code, response.Body.String())
	}
}
