package web

import (
	"encoding/json"
	"net/http"
	"net/url"
)

func urlQuery(s string) string { return url.QueryEscape(s) }

func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="dynid-export.json"`)
	if err := s.st.Export(w); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		http.Redirect(w, r, "/?err="+urlQuery("无法读取上传文件: "+err.Error()), http.StatusSeeOther)
		return
	}
	confirm := r.FormValue("confirm_clear") == "1"
	if !confirm {
		http.Redirect(w, r, "/?err="+urlQuery("导入会先清空数据库，必须勾选确认"), http.StatusSeeOther)
		return
	}
	f, _, err := r.FormFile("bundle")
	if err != nil {
		http.Redirect(w, r, "/?err="+urlQuery("请选择导出的 JSON 文件"), http.StatusSeeOther)
		return
	}
	defer f.Close()
	nd, nr, err := s.st.Import(f)
	if err != nil {
		http.Redirect(w, r, "/?err="+urlQuery("导入失败: "+err.Error()), http.StatusSeeOther)
		return
	}
	msg := "已清空并重新导入：数据集 " + itoa(nd) + " 个，运行记录 " + itoa(nr) + " 条（原报告原样复核，未重算）"
	http.Redirect(w, r, "/?flash=imported&info="+urlQuery(msg), http.StatusSeeOther)
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("confirm_clear") != "1" {
		http.Redirect(w, r, "/?err="+urlQuery("清空数据库必须勾选确认"), http.StatusSeeOther)
		return
	}
	if err := s.st.Reset(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/?flash=imported&info="+urlQuery("数据库已清空"), http.StatusSeeOther)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}
