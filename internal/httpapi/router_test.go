package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/vancemichael/092002-medical-collaboration-control/internal/store"
)

// newTestHandler 为每个测试用例建立独立的临时数据库。
func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return Router(st)
}

// call 发起带身份头的请求并返回记录器。
func call(t *testing.T, handler http.Handler, method, path string, body any, id Identity) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求体失败: %v", err)
		}
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	if id.InstitutionRef != "" {
		request.Header.Set("X-Institution-Ref", id.InstitutionRef)
	}
	if id.ActorRef != "" {
		request.Header.Set("X-Actor-Ref", id.ActorRef)
	}
	if id.Role != "" {
		request.Header.Set("X-Actor-Role", id.Role)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// decode 解析响应体到 map。
func decode(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败: %v，原文 %s", err, response.Body.String())
	}
	return out
}

func TestHealth(t *testing.T) {
	response := call(t, newTestHandler(t), http.MethodGet, "/health", nil, Identity{})
	if response.Code != http.StatusOK {
		t.Fatalf("健康接口状态码为 %d", response.Code)
	}
}
