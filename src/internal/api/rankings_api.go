// 文件: src/internal/api/rankings_api.go
package api

import (
	"net/http"

	"llm-hub/src/internal/rank"
)

// rankings 返回绝对真实的模型排行榜（只读公开端点，不含任何编造数据）。
//   GET /api/rankings             → 全部维度合集
//   GET /api/rankings/{dimension} → 单个维度的完整榜单
//   GET /api/rankings?verify=1    → 真实性复核结论（每条 id ∈ data/models-real.json）
func (s *Server) rankings(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("verify") == "1" {
		v, err := rank.Verify()
		if err != nil {
			fail(w, r, http.StatusInternalServerError, "upstream_error", "ranking_verify_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
		return
	}

	dim := r.PathValue("dimension")
	if dim == "" {
		rep, err := rank.All()
		if err != nil {
			fail(w, r, http.StatusInternalServerError, "upstream_error", "ranking_build_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rep)
		return
	}

	b, err := rank.Compute(dim)
	if err != nil {
		fail(w, r, http.StatusNotFound, "invalid_request_error", "unknown_ranking_dimension", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, b)
}
