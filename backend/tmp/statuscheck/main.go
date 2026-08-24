// 终验脚本：并发抢单后的状态核验（阶段4 验收配套）。
// ① 每个用户查 /status → 统计 true/false 分布（= Redis members 真实容量）
// ② 每个未抢到的用户重抢一次 → 应全部 SOLD_OUT（= stock=0 的证据）
// 用法：go run ./tmp/statuscheck <good_id>
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

const (
	base     = "http://localhost:8080/api/v1"
	password = "abc12345"
	nUsers   = 20
)

// post 通用 POST
func post(url string, body any, token string) map[string]any {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return map[string]any{"code": float64(-1)}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

// get GET with token
func get(url, token string) map[string]any {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return map[string]any{"code": float64(-1)}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func main() {
	goodID := os.Args[1]
	statusURL := fmt.Sprintf("%s/group-buy/%s/status", base, goodID)
	grabURL := fmt.Sprintf("%s/group-buy/%s/grab", base, goodID)

	grabbed, notGrabbed := 0, 0
	retryResults := map[string]int{}
	for i := 1; i <= nUsers; i++ {
		user := fmt.Sprintf("loadtest%02d", i)
		r := post(base+"/auth/login", map[string]string{"username": user, "password": password}, "")
		data, _ := r["data"].(map[string]any)
		if data == nil {
			continue
		}
		token := data["token"].(string)
		// ① 查轮询状态
		s := get(statusURL, token)
		if d, ok := s["data"].(map[string]any); ok && d["grabbed"] == true {
			grabbed++
		} else {
			notGrabbed++
			// ② 未抢到的重抢一次：此刻 stock 应为 0 → 预期 SOLD_OUT(20004)
			g := post(grabURL, map[string]string{}, token)
			code := fmt.Sprintf("%v", g["code"])
			switch code {
			case "20004":
				retryResults["SOLD_OUT"]++
			case "20007":
				retryResults["BUSY"]++
			case "20005":
				retryResults["DUPLICATE"]++
			case "0":
				retryResults["OK(!!超卖!!)"]++
			default:
				retryResults["OTHER:"+code]++
			}
		}
	}
	fmt.Printf("status check: grabbed=%d not_grabbed=%d (期望 5/15)\n", grabbed, notGrabbed)
	fmt.Println("retry grab by not-grabbed users (期望全部 SOLD_OUT):")
	for k, v := range retryResults {
		fmt.Printf("  %-14s x %d\n", k, v)
	}
}
