package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/vancemichael/092002-medical-collaboration-control/internal/terminology"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	store, err := terminology.Open(filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return New(terminology.NewService(store))
}

type actor struct{ institution, role string }

var (
	stewardA  = actor{"HOSPITAL-A", "data-steward"}
	stewardB  = actor{"HOSPITAL-B", "data-steward"}
	leadA     = actor{"HOSPITAL-A", "clinical-lead"}
	leadB     = actor{"HOSPITAL-B", "clinical-lead"}
	center    = actor{"LEAD-CENTER", "lead-center"}
	regulator = actor{"REGULATOR", "regulator"}
)

func call(t *testing.T, handler http.Handler, who *actor, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("编码请求体失败: %v", err)
		}
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	if who != nil {
		request.Header.Set("X-Actor-Institution", who.institution)
		request.Header.Set("X-Actor-Role", who.role)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	decoded := map[string]any{}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("响应不是合法 JSON: %v, 内容: %s", err, response.Body.String())
	}
	return response.Code, decoded
}

func mustSubmit(t *testing.T, handler http.Handler, who actor, localField, language, unit string) string {
	t.Helper()
	status, body := call(t, handler, &who, http.MethodPost, "/term-submissions", map[string]any{
		"institution_ref":   who.institution,
		"local_field_ref":   localField,
		"definition_digest": "sha256:" + fmt.Sprintf("%064x", len(localField)),
		"language":          language,
		"unit":              unit,
		"purposes":          []string{"quality-comparison"},
	})
	if status != http.StatusCreated {
		t.Fatalf("提交字段定义失败: 状态 %d, 响应 %v", status, body)
	}
	return body["submission_ref"].(string)
}

// 走通 提交→映射→双方确认→生效 的最短路径，返回 mapping_ref。
func mustEffectiveMapping(t *testing.T, handler http.Handler, indicatorKey string) string {
	t.Helper()
	left := mustSubmit(t, handler, stewardA, "fall-rate", "zh-CN", "%")
	right := mustSubmit(t, handler, stewardB, "fall_incidence", "en", "ratio")
	status, body := call(t, handler, &stewardA, http.MethodPost, "/mappings", map[string]any{
		"indicator_key":        indicatorKey,
		"left_submission_ref":  left,
		"right_submission_ref": right,
		"equivalence_note":     "双方均按住院患者跌倒例次/千床日统计",
	})
	if status != http.StatusCreated {
		t.Fatalf("发起映射失败: 状态 %d, 响应 %v", status, body)
	}
	mappingRef := body["mapping"].(map[string]any)["mapping_ref"].(string)
	for _, who := range []actor{leadA, leadB} {
		status, body := call(t, handler, &who, http.MethodPost,
			"/mappings/"+mappingRef+"/versions/1/confirmations", map[string]any{})
		if status != http.StatusOK {
			t.Fatalf("确认映射失败: 状态 %d, 响应 %v", status, body)
		}
	}
	return mappingRef
}

func mustGrant(t *testing.T, handler http.Handler, taskRef, indicatorKey, mappingRef string, version int) {
	t.Helper()
	status, body := call(t, handler, &center, http.MethodPost, "/tasks/"+taskRef+"/catalog-grants", map[string]any{
		"indicator_key": indicatorKey,
		"mapping_ref":   mappingRef,
		"version":       version,
	})
	if status != http.StatusCreated {
		t.Fatalf("发放目录失败: 状态 %d, 响应 %v", status, body)
	}
}

func catalogEntries(t *testing.T, handler http.Handler, who actor, taskRef string) []any {
	t.Helper()
	status, body := call(t, handler, &who, http.MethodGet, "/tasks/"+taskRef+"/catalog", nil)
	if status != http.StatusOK {
		t.Fatalf("查询目录失败: 状态 %d, 响应 %v", status, body)
	}
	return body["entries"].([]any)
}

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	newTestHandler(t).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("健康接口状态码为 %d", response.Code)
	}
}

func TestTermAgreementFlow(t *testing.T) {
	handler := newTestHandler(t)
	mappingRef := mustEffectiveMapping(t, handler, "NUR-FALL-RATE")
	mustGrant(t, handler, "TASK-1", "NUR-FALL-RATE", mappingRef, 1)

	entries := catalogEntries(t, handler, stewardA, "TASK-1")
	if len(entries) != 1 {
		t.Fatalf("合作方应看到 1 条目录，实际 %d", len(entries))
	}
	entry := entries[0].(map[string]any)
	if entry["status"] != "active" {
		t.Fatalf("目录条目状态应为 active，实际 %v", entry["status"])
	}
	if entry["left"].(map[string]any)["definition_digest"] == "" {
		t.Fatal("目录应携带两侧定义的不可逆摘要")
	}

	// 非当事机构查询同一任务，看不到任何映射。
	outsider := actor{"HOSPITAL-C", "data-steward"}
	if entries := catalogEntries(t, handler, outsider, "TASK-1"); len(entries) != 0 {
		t.Fatalf("非当事机构不应看到目录内容，实际 %d 条", len(entries))
	}
}

func TestGrantRequiresDualConfirmation(t *testing.T) {
	handler := newTestHandler(t)
	left := mustSubmit(t, handler, stewardA, "fall-rate", "zh-CN", "%")
	right := mustSubmit(t, handler, stewardB, "fall_incidence", "en", "ratio")
	_, body := call(t, handler, &stewardA, http.MethodPost, "/mappings", map[string]any{
		"indicator_key":        "NUR-FALL-RATE",
		"left_submission_ref":  left,
		"right_submission_ref": right,
		"equivalence_note":     "口径一致",
	})
	mappingRef := body["mapping"].(map[string]any)["mapping_ref"].(string)

	// 仅一方确认时不能发放。
	call(t, handler, &leadA, http.MethodPost, "/mappings/"+mappingRef+"/versions/1/confirmations", map[string]any{})
	status, _ := call(t, handler, &center, http.MethodPost, "/tasks/TASK-1/catalog-grants", map[string]any{
		"indicator_key": "NUR-FALL-RATE",
		"mapping_ref":   mappingRef,
		"version":       1,
	})
	if status != http.StatusConflict {
		t.Fatalf("未双方确认的版本不应发放，状态码 %d", status)
	}

	// 同一机构重复签署应被拒绝。
	status, _ = call(t, handler, &leadA, http.MethodPost, "/mappings/"+mappingRef+"/versions/1/confirmations", map[string]any{})
	if status != http.StatusConflict {
		t.Fatalf("重复签署应返回 409，实际 %d", status)
	}
}

func TestOverturnKeepsHistory(t *testing.T) {
	handler := newTestHandler(t)
	mappingRef := mustEffectiveMapping(t, handler, "NUR-FALL-RATE")
	mustGrant(t, handler, "TASK-1", "NUR-FALL-RATE", mappingRef, 1)

	// 术语翻译被推翻：旧版本状态推进，但签署与发放记录保留。
	status, _ := call(t, handler, &leadB, http.MethodPost, "/mappings/"+mappingRef+"/versions/1/overturn", map[string]any{
		"reason": "泰语译名实际包含门诊随访，口径不一致",
	})
	if status != http.StatusOK {
		t.Fatalf("推翻失败: 状态 %d", status)
	}
	entries := catalogEntries(t, handler, stewardA, "TASK-1")
	if entries[0].(map[string]any)["status"] != "overturned" {
		t.Fatalf("被推翻后目录状态应为 overturned，实际 %v", entries[0])
	}

	// 修订出新版本并重新双方确认、发放；修订后的字段定义以新提交登记。
	left := mustSubmit(t, handler, stewardA, "fall-rate-inpatient", "zh-CN", "%")
	right := mustSubmit(t, handler, stewardB, "fall_incidence_inpatient", "en", "ratio")
	status, body := call(t, handler, &stewardB, http.MethodPost, "/mappings/"+mappingRef+"/versions", map[string]any{
		"left_submission_ref":  left,
		"right_submission_ref": right,
		"equivalence_note":     "修订后仅统计住院场景",
	})
	if status != http.StatusCreated {
		t.Fatalf("追加版本失败: 状态 %d, 响应 %v", status, body)
	}
	for _, who := range []actor{leadA, leadB} {
		call(t, handler, &who, http.MethodPost, "/mappings/"+mappingRef+"/versions/2/confirmations", map[string]any{})
	}
	mustGrant(t, handler, "TASK-1", "NUR-FALL-RATE", mappingRef, 2)

	entries = catalogEntries(t, handler, stewardA, "TASK-1")
	entry := entries[0].(map[string]any)
	if entry["status"] != "active" || entry["granted_version"].(float64) != 2 {
		t.Fatalf("新版本发放后应为 active 且版本为 2，实际 %v", entry)
	}

	// 监管溯源：两个版本都在，旧版本的签署与发放记录仍可查。
	status, body = call(t, handler, &regulator, http.MethodGet, "/indicators/NUR-FALL-RATE/lineage", nil)
	if status != http.StatusOK {
		t.Fatalf("监管溯源失败: 状态 %d", status)
	}
	versions := body["mappings"].([]any)[0].(map[string]any)["versions"].([]any)
	if len(versions) != 2 {
		t.Fatalf("溯源应包含 2 个版本，实际 %d", len(versions))
	}
	v1 := versions[0].(map[string]any)
	if v1["version"].(map[string]any)["status"] != "overturned" {
		t.Fatalf("旧版本状态应为 overturned，实际 %v", v1["version"])
	}
	if len(v1["confirmations"].([]any)) != 2 {
		t.Fatal("旧版本的双方签署记录必须保留")
	}
	if len(v1["grants"].([]any)) != 1 {
		t.Fatal("旧版本的发放记录必须保留")
	}
}

func TestWithdrawalScopeVisibleToRegulator(t *testing.T) {
	handler := newTestHandler(t)
	mappingRef := mustEffectiveMapping(t, handler, "NUR-FALL-RATE")
	mustGrant(t, handler, "TASK-1", "NUR-FALL-RATE", mappingRef, 1)
	mustGrant(t, handler, "TASK-2", "NUR-FALL-RATE", mappingRef, 1)

	// B 方撤回，范围仅限 TASK-1。
	status, _ := call(t, handler, &leadB, http.MethodPost, "/mappings/"+mappingRef+"/versions/1/withdrawals", map[string]any{
		"scope":  map[string]any{"tasks": []string{"TASK-1"}},
		"reason": "本机构授权用途变更",
	})
	if status != http.StatusCreated {
		t.Fatalf("撤回失败: 状态 %d", status)
	}

	if entries := catalogEntries(t, handler, stewardA, "TASK-1"); entries[0].(map[string]any)["status"] != "withdrawn" {
		t.Fatal("撤回范围内的任务应显示 withdrawn")
	}
	if entries := catalogEntries(t, handler, stewardA, "TASK-2"); entries[0].(map[string]any)["status"] != "active" {
		t.Fatal("撤回范围外的任务应保持 active")
	}

	// 监管沿指标可看到撤回机构与范围。
	_, body := call(t, handler, &regulator, http.MethodGet, "/indicators/NUR-FALL-RATE/lineage", nil)
	withdrawals := body["mappings"].([]any)[0].(map[string]any)["versions"].([]any)[0].(map[string]any)["withdrawals"].([]any)
	if len(withdrawals) != 1 {
		t.Fatal("溯源应包含撤回记录")
	}
	withdrawal := withdrawals[0].(map[string]any)
	if withdrawal["institution_ref"] != "HOSPITAL-B" {
		t.Fatalf("撤回机构应为 HOSPITAL-B，实际 %v", withdrawal["institution_ref"])
	}
	scope := withdrawal["scope"].(map[string]any)
	if scope["tasks"].([]any)[0] != "TASK-1" {
		t.Fatalf("撤回范围应包含 TASK-1，实际 %v", scope)
	}
}

func TestRejectsUnexpectedFieldsAndRawDefinitions(t *testing.T) {
	handler := newTestHandler(t)

	// 契约之外的字段（例如试图上传病历内容）一律拒绝。
	status, body := call(t, handler, &stewardA, http.MethodPost, "/term-submissions", map[string]any{
		"institution_ref":   "HOSPITAL-A",
		"local_field_ref":   "fall-rate",
		"definition_digest": "sha256:" + fmt.Sprintf("%064x", 1),
		"language":          "zh-CN",
		"unit":              "%",
		"purposes":          []string{"quality-comparison"},
		"patient_record":    "name: 张三, diagnosis: ...",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("携带契约外字段应返回 400，实际 %d，响应 %v", status, body)
	}

	// 定义原文不是不可逆摘要时拒绝。
	status, _ = call(t, handler, &stewardA, http.MethodPost, "/term-submissions", map[string]any{
		"institution_ref":   "HOSPITAL-A",
		"local_field_ref":   "fall-rate",
		"definition_digest": "跌倒率=跌倒例次/住院床日数",
		"language":          "zh-CN",
		"unit":              "%",
		"purposes":          []string{"quality-comparison"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("非摘要形式的定义应返回 400，实际 %d", status)
	}
}

func TestAuthorizationRules(t *testing.T) {
	handler := newTestHandler(t)
	mappingRef := mustEffectiveMapping(t, handler, "NUR-FALL-RATE")
	mustGrant(t, handler, "TASK-1", "NUR-FALL-RATE", mappingRef, 1)

	cases := []struct {
		name   string
		who    *actor
		method string
		path   string
		body   any
		want   int
	}{
		{"缺少身份头", nil, http.MethodGet, "/tasks/TASK-1/catalog", nil, http.StatusForbidden},
		{"临床负责人不能提交字段", &leadA, http.MethodPost, "/term-submissions", map[string]any{
			"institution_ref": "HOSPITAL-A", "local_field_ref": "x",
			"definition_digest": "sha256:" + fmt.Sprintf("%064x", 9),
			"language":          "zh-CN", "unit": "%", "purposes": []string{"q"},
		}, http.StatusForbidden},
		{"不能代他机构提交", &stewardB, http.MethodPost, "/term-submissions", map[string]any{
			"institution_ref": "HOSPITAL-A", "local_field_ref": "y",
			"definition_digest": "sha256:" + fmt.Sprintf("%064x", 8),
			"language":          "en", "unit": "%", "purposes": []string{"q"},
		}, http.StatusForbidden},
		{"监管不能查合作方目录", &regulator, http.MethodGet, "/tasks/TASK-1/catalog", nil, http.StatusForbidden},
		{"合作方不能查监管溯源", &stewardA, http.MethodGet, "/indicators/NUR-FALL-RATE/lineage", nil, http.StatusForbidden},
		{"非当事方不能确认", &actor{"HOSPITAL-C", "clinical-lead"}, http.MethodPost,
			"/mappings/" + mappingRef + "/versions/1/confirmations", map[string]any{}, http.StatusForbidden},
		{"机构不能看他机构提交", &stewardB, http.MethodGet, "/term-submissions?institution_ref=HOSPITAL-A", nil, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := call(t, handler, tc.who, tc.method, tc.path, tc.body)
			if status != tc.want {
				t.Fatalf("状态码应为 %d，实际 %d", tc.want, status)
			}
		})
	}
}

func TestAmbiguityNotesTravelWithCatalog(t *testing.T) {
	handler := newTestHandler(t)
	mappingRef := mustEffectiveMapping(t, handler, "NUR-FALL-RATE")
	mustGrant(t, handler, "TASK-1", "NUR-FALL-RATE", mappingRef, 1)

	status, _ := call(t, handler, &stewardB, http.MethodPost, "/mappings/"+mappingRef+"/versions/1/ambiguity-notes", map[string]any{
		"body": "越语译名在部分省份含家庭病床场景，比较时需剔除",
	})
	if status != http.StatusCreated {
		t.Fatalf("登记歧义说明失败: 状态 %d", status)
	}
	entries := catalogEntries(t, handler, stewardA, "TASK-1")
	notes := entries[0].(map[string]any)["ambiguity_notes"].([]any)
	if len(notes) != 1 || notes[0].(map[string]any)["author_institution_ref"] != "HOSPITAL-B" {
		t.Fatalf("目录应携带歧义说明及作者机构，实际 %v", notes)
	}
}
