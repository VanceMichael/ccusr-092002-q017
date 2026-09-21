package httpapi

import (
	"net/http"

	"github.com/vancemichael/092002-medical-collaboration-control/internal/store"
)

// Router 返回术语与用途协议服务的 HTTP 路由。
func Router(st *store.Store) http.Handler {
	h := &handlers{store: st}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.health)

	mux.HandleFunc("POST /terms", h.createTerm)
	mux.HandleFunc("GET /terms/{termID}", h.getTerm)
	mux.HandleFunc("POST /terms/{termID}/withdraw", h.withdrawTerm)

	mux.HandleFunc("POST /mappings", h.createMapping)
	mux.HandleFunc("GET /mappings/{mappingID}", h.getMapping)
	mux.HandleFunc("POST /mappings/{mappingID}/versions", h.createVersion)
	mux.HandleFunc("POST /mappings/{mappingID}/versions/{versionNo}/confirm", h.confirmVersion)
	mux.HandleFunc("POST /mappings/{mappingID}/versions/{versionNo}/reject", h.rejectVersion)

	mux.HandleFunc("POST /catalogs", h.createCatalog)
	mux.HandleFunc("GET /catalogs/{catalogID}", h.getCatalog)
	mux.HandleFunc("GET /tasks/{taskRef}/catalog", h.getTaskCatalog)

	mux.HandleFunc("GET /catalogs/{catalogID}/entries/{indicatorKey}/lineage", h.getIndicatorLineage)
	return mux
}
