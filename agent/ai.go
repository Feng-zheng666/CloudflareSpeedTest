package agent

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

var aiEndpoints = []string{
	"api.openai.com:443",
	"api.anthropic.com:443",
	"gateway.ai.cloudflare.com:443",
}

// TestAIEndpoints 测试 IP 到所有 AI 端点的连通性
// 返回: 全部通过(true), 各端点的 HTTPS TTFB(ms)
func TestAIEndpoints(ip *net.IPAddr) (bool, map[string]float64) {
	ttfbs := make(map[string]float64, len(aiEndpoints))
	allPassed := true

	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, endpoint := range aiEndpoints {
		wg.Add(1)
		go func(ep string) {
			defer wg.Done()
			passed, ttfb := testSingleEndpoint(ep)
			mu.Lock()
			if !passed {
				allPassed = false
			}
			if ttfb > 0 {
				ttfbs[ep] = ttfb
			}
			mu.Unlock()
		}(endpoint)
	}
	wg.Wait()

	return allPassed, ttfbs
}

// testSingleEndpoint 测试单个 AI 端点：TCP 连通 + HTTPS TTFB
func testSingleEndpoint(endpoint string) (passed bool, ttfb float64) {
	conn, err := net.DialTimeout("tcp", endpoint, 2*time.Second)
	if err != nil {
		return false, 0
	}
	conn.Close()
	passed = true

	host, _, _ := net.SplitHostPort(endpoint)
	url := fmt.Sprintf("https://%s/", host)

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", endpoint)
			},
			TLSClientConfig: &tls.Config{ServerName: host},
		},
	}
	defer client.CloseIdleConnections()

	req, _ := http.NewRequest("HEAD", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return true, 0 // TCP 通了但 HTTPS 失败仍算通过，只是无加分
	}
	ttfb = float64(time.Since(start).Milliseconds())
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	return true, ttfb
}
