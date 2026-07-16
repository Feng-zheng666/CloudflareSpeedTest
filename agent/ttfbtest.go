package agent

import (
	"io"
	"net"
	"net/http"
	"time"

	"github.com/XIU2/CloudflareSpeedTest/task"
)

// MeasureTTFB 测量目标 IP 对指定 URL 的首字节时间（TTFB, ms）
// 通过自定义 DialContext 将请求绑定到指定 IP
func MeasureTTFB(ip *net.IPAddr, url string) float64 {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: task.GetDialContext(ip),
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // 不跟随重定向
		},
	}
	defer client.CloseIdleConnections()

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/98.0.4758.80 Safari/537.36")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	ttfb := float64(time.Since(start).Milliseconds())
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	return ttfb
}
