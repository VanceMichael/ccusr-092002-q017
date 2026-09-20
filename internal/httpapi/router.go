// Package httpapi 暴露跨机构术语与用途协议的 HTTP 接口。
//
// 调用方身份通过请求头携带：X-Actor-Institution 为机构编号，
// X-Actor-Role 为角色（data-steward / clinical-lead / lead-center / regulator）。
// 所有写接口严格按契约字段解码，多出的字段一律拒绝，
// 保证中心不会误收契约之外的数据（包括任何原始病历内容）。
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/vancemichael/092002-medical-collaboration-control/internal/terminology"
)

// New 构造路由；svc 承载全部领域用例。
func New(svc *terminology.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.Handle("POST /term-submissions", handler(svc, submitTerm))
	mux.Handle("GET /term-submissions", handler(svc, listTerms))
	mux.Handle("POST /mappings", handler(svc, proposeMapping))
	mux.Handle("GET /mappings/{mappingRef}", handler(svc, getMapping))
	mux.Handle("POST /mappings/{mappingRef}/versions", handler(svc, proposeVersion))
	mux.Handle("POST /mappings/{mappingRef}/versions/{version}/confirmations", handler(svc, confirm))
	mux.Handle("POST /mappings/{mappingRef}/versions/{version}/overturn", handler(svc, overturn))
	mux.Handle("POST /mappings/{mappingRef}/versions/{version}/withdrawals", handler(svc, withdraw))
	mux.Handle("POST /mappings/{mappingRef}/versions/{version}/ambiguity-notes", handler(svc, addNote))
	mux.Handle("POST /tasks/{taskRef}/catalog-grants", handler(svc, grantCatalog))
	mux.Handle("GET /tasks/{taskRef}/catalog", handler(svc, taskCatalog))
	mux.Handle("GET /indicators/{indicatorKey}/lineage", handler(svc, indicatorLineage))
	return mux
}

// endpoint 是带服务依赖与调用方身份的处理函数。
type endpoint func(svc *terminology.Service, actor terminology.Actor,
	response http.ResponseWriter, request *http.Request)

// handler 统一完成身份解析、错误映射与响应编码。
func handler(svc *terminology.Service, fn endpoint) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		actor, err := actorFrom(request)
		if err != nil {
			writeError(response, err)
			return
		}
		fn(svc, actor, response, request)
	})
}

func actorFrom(request *http.Request) (terminology.Actor, error) {
	actor := terminology.Actor{
		Institution: request.Header.Get("X-Actor-Institution"),
		Role:        request.Header.Get("X-Actor-Role"),
	}
	if actor.Institution == "" || actor.Role == "" {
		return actor, &terminology.Error{
			Kind: terminology.KindForbidden,
			Msg:  "缺少 X-Actor-Institution 或 X-Actor-Role 请求头",
		}
	}
	switch actor.Role {
	case terminology.RoleDataSteward, terminology.RoleClinicalLead,
		terminology.RoleLeadCenter, terminology.RoleRegulator:
	default:
		return actor, &terminology.Error{
			Kind: terminology.KindForbidden,
			Msg:  "未知的 X-Actor-Role 角色",
		}
	}
	return actor, nil
}

// decode 严格按契约解码请求体：未知字段直接拒绝。
func decode(response http.ResponseWriter, request *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(response, &terminology.Error{
			Kind: terminology.KindValidation,
			Msg:  "请求体不符合字段约定: " + err.Error(),
		})
		return false
	}
	return true
}

func pathVersion(request *http.Request) (int, bool) {
	version, err := strconv.Atoi(request.PathValue("version"))
	if err != nil || version < 1 {
		return 0, false
	}
	return version, true
}

func writeJSON(response http.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}

func writeError(response http.ResponseWriter, err error) {
	var domainErr *terminology.Error
	status := http.StatusInternalServerError
	if errors.As(err, &domainErr) {
		switch domainErr.Kind {
		case terminology.KindValidation:
			status = http.StatusBadRequest
		case terminology.KindForbidden:
			status = http.StatusForbidden
		case terminology.KindNotFound:
			status = http.StatusNotFound
		case terminology.KindConflict:
			status = http.StatusConflict
		}
	}
	writeJSON(response, status, map[string]string{"error": err.Error()})
}

func submitTerm(svc *terminology.Service, actor terminology.Actor,
	response http.ResponseWriter, request *http.Request) {
	var input terminology.SubmitTermInput
	if !decode(response, request, &input) {
		return
	}
	submission, err := svc.SubmitTerm(actor, input)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, submission)
}

func listTerms(svc *terminology.Service, actor terminology.Actor,
	response http.ResponseWriter, request *http.Request) {
	submissions, err := svc.ListSubmissions(actor, request.URL.Query().Get("institution_ref"))
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"submissions": submissions})
}

func proposeMapping(svc *terminology.Service, actor terminology.Actor,
	response http.ResponseWriter, request *http.Request) {
	var input terminology.ProposeMappingInput
	if !decode(response, request, &input) {
		return
	}
	mapping, version, err := svc.ProposeMapping(actor, input)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"mapping": mapping, "version": version})
}

func getMapping(svc *terminology.Service, actor terminology.Actor,
	response http.ResponseWriter, request *http.Request) {
	mapping, versions, err := svc.GetMapping(actor, request.PathValue("mappingRef"))
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"mapping": mapping, "versions": versions})
}

func proposeVersion(svc *terminology.Service, actor terminology.Actor,
	response http.ResponseWriter, request *http.Request) {
	var input terminology.ProposeVersionInput
	if !decode(response, request, &input) {
		return
	}
	version, err := svc.ProposeVersion(actor, request.PathValue("mappingRef"), input)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, version)
}

func confirm(svc *terminology.Service, actor terminology.Actor,
	response http.ResponseWriter, request *http.Request) {
	version, ok := pathVersion(request)
	if !ok {
		writeError(response, &terminology.Error{Kind: terminology.KindValidation, Msg: "version 路径参数必须是正整数"})
		return
	}
	var input struct {
		SignatoryRef string `json:"signatory_ref"`
	}
	if request.ContentLength > 0 && !decode(response, request, &input) {
		return
	}
	updated, err := svc.Confirm(actor, request.PathValue("mappingRef"), version, input.SignatoryRef)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, updated)
}

func overturn(svc *terminology.Service, actor terminology.Actor,
	response http.ResponseWriter, request *http.Request) {
	version, ok := pathVersion(request)
	if !ok {
		writeError(response, &terminology.Error{Kind: terminology.KindValidation, Msg: "version 路径参数必须是正整数"})
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !decode(response, request, &input) {
		return
	}
	updated, err := svc.Overturn(actor, request.PathValue("mappingRef"), version, input.Reason)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, updated)
}

func withdraw(svc *terminology.Service, actor terminology.Actor,
	response http.ResponseWriter, request *http.Request) {
	version, ok := pathVersion(request)
	if !ok {
		writeError(response, &terminology.Error{Kind: terminology.KindValidation, Msg: "version 路径参数必须是正整数"})
		return
	}
	var input struct {
		Scope  terminology.WithdrawalScope `json:"scope"`
		Reason string                      `json:"reason"`
	}
	if !decode(response, request, &input) {
		return
	}
	withdrawal, err := svc.Withdraw(actor, request.PathValue("mappingRef"), version, input.Scope, input.Reason)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, withdrawal)
}

func addNote(svc *terminology.Service, actor terminology.Actor,
	response http.ResponseWriter, request *http.Request) {
	version, ok := pathVersion(request)
	if !ok {
		writeError(response, &terminology.Error{Kind: terminology.KindValidation, Msg: "version 路径参数必须是正整数"})
		return
	}
	var input struct {
		Body string `json:"body"`
	}
	if !decode(response, request, &input) {
		return
	}
	note, err := svc.AddAmbiguityNote(actor, request.PathValue("mappingRef"), version, input.Body)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, note)
}

func grantCatalog(svc *terminology.Service, actor terminology.Actor,
	response http.ResponseWriter, request *http.Request) {
	var input struct {
		IndicatorKey string `json:"indicator_key"`
		MappingRef   string `json:"mapping_ref"`
		Version      int    `json:"version"`
	}
	if !decode(response, request, &input) {
		return
	}
	grant, err := svc.GrantCatalog(actor, request.PathValue("taskRef"),
		input.IndicatorKey, input.MappingRef, input.Version)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, grant)
}

func taskCatalog(svc *terminology.Service, actor terminology.Actor,
	response http.ResponseWriter, request *http.Request) {
	catalog, err := svc.TaskCatalog(actor, request.PathValue("taskRef"))
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, catalog)
}

func indicatorLineage(svc *terminology.Service, actor terminology.Actor,
	response http.ResponseWriter, request *http.Request) {
	lineage, err := svc.IndicatorLineage(actor, request.PathValue("indicatorKey"))
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, lineage)
}
