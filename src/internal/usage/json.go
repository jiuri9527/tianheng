package usage

import (
	"encoding/json"
	"io"
)

// jsonEncode 用标准库编码并保留 HTML 转义关闭（聚合数字无需转义）。
func jsonEncode(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	return enc.Encode(v)
}
