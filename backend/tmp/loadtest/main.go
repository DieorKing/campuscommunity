// 一次性压测脚本：N 个用户并发抢同一个拼单，验证零超卖（阶段4 验收）。
// 放在 tmp/（gitignored）：验证完即弃，正式压测在阶段9 用 wrk。
// 用法：go run ./tmp/loadtest <good_id>
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	base     = "http://localhost:8080/api/v1"
	password = "abc12345"
	nUsers   = 20
)

// postJSON 通用 POST：带可选 token，返回解析后的 JSON
func postJSON(url string, body any, token string) map[string]any {
	b, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", url, bytes.NewReader(b))
	if err != nil {
		return map[string]any{"code": float64(-1), "msg": err.Error()}
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return map[string]any{"code": float64(-1), "msg": err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: go run ./tmp/loadtest <good_id>")
		os.Exit(1)
	}
	goodID := os.Args[1]
	grabURL := fmt.Sprintf("%s/group-buy/%s/grab", base, goodID)

	// 1. 注册 + 登录 20 个用户（顺序执行，注册幂等：已存在 10001 忽略）
	tokens := make([]string, 0, nUsers)
	for i := 1; i <= nUsers; i++ {
		user := fmt.Sprintf("loadtest%02d", i)
		postJSON(base+"/auth/register", map[string]string{
			"username": user, "password": password, "confirm_password": password,
		}, "")
		r := postJSON(base+"/auth/login", map[string]string{
			"username": user, "password": password,
		}, "")
		if data, ok := r["data"].(map[string]any); ok {
			tokens = append(tokens, data["token"].(string))
		}
	}
	fmt.Printf("prepared %d users\n", len(tokens))

	// 2. 并发抢单：20 个 goroutine 同时开火，WaitGroup 等全部返回
	start := time.Now()
	results := make([]string, len(tokens))
	var wg sync.WaitGroup
	var mu sync.Mutex
	counts := map[string]int{}
	for i, tk := range tokens {
		wg.Add(1)
		go func(idx int, token string) {
			defer wg.Done()
			r := postJSON(grabURL, map[string]string{}, token)
			code := fmt.Sprintf("%v", r["code"])
			var desc string
			switch code {
			case "0":
				desc = "OK(grabbed)"
			case "20004":
				desc = "SOLD_OUT"
			case "20007":
				desc = "BUSY"
			case "20005":
				desc = "DUPLICATE"
			default:
				desc = fmt.Sprintf("OTHER(%v %v)", r["code"], r["msg"])
			}
			mu.Lock()
			counts[desc]++
			mu.Unlock()
			results[idx] = desc
		}(i, tk)
	}
	wg.Wait()
	elapsed := time.Since(start)

	// 3. 汇总
	fmt.Printf("all %d requests done in %v\n", len(tokens), elapsed)
	for k, v := range counts {
		fmt.Printf("  %-14s x %d\n", k, v)
	}
}
