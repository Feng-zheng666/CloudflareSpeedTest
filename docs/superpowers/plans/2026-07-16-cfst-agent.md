# CFST-Agent 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**目标:** 在现有 CloudflareSpeedTest 仓库中新增 `cfst-agent` 独立二进制，对原 CFST 输出的 IP 列表做二阶段深度评估和百分制打分。

**架构:** 同仓库新增 `cmd/agent/main.go` CLI 入口 + `agent/` 包（7 文件），复用现有 `task` 包中已导出的 TCPing/HTTPing 辅助函数，新增 `data/` 包嵌入 ip2region.xdb 数据库。

**技术栈:** Go 1.18+, ip2region v2, 复用现有 fatih/color + cheggaaa/pb

---

### Task 1: 项目骨架搭建 — 目录、依赖、数据库下载

**Files:**
- Create: `cmd/agent/main.go`
- Create: `agent/evaluate.go`
- Create: `agent/tcpdeep.go`
- Create: `agent/ttfbtest.go`
- Create: `agent/geoip.go`
- Create: `agent/ai.go`
- Create: `agent/scorer.go`
- Create: `agent/output.go`
- Create: `data/embed.go`
- Modify: `go.mod`

- [ ] **Step 1: 创建目录结构**

```bash
mkdir -p cmd/agent agent data
```

- [ ] **Step 2: 下载 ip2region.xdb 数据库文件**

```bash
curl -L -o data/ip2region.xdb https://github.com/lionsoul2014/ip2region/raw/master/data/ip2region.xdb
```

- [ ] **Step 3: 添加 ip2region 依赖**

```bash
go get github.com/lionsoul2014/ip2region/v2@latest
```

- [ ] **Step 4: 创建 data/embed.go — 内嵌 xdb 文件**

```go
package data

import _ "embed"

//go:embed ip2region.xdb
var IP2RegionDB []byte
```

- [ ] **Step 5: 创建所有 agent/*.go 占位文件**

每个文件写入正确的 `package agent` 声明即可（内容在后续任务填充）：

```bash
for f in evaluate tcpdeep ttfbtest geoip ai scorer output; do
  echo "package agent" > "agent/${f}.go"
done
```

- [ ] **Step 6: 创建 cmd/agent/main.go 占位**

```go
package main

func main() {}
```

- [ ] **Step 7: 验证编译通过**

```bash
go build ./cmd/agent/...
```
Expected: 编译成功（空 main）

- [ ] **Step 8: Commit**

```bash
git add cmd/ agent/ data/ go.mod go.sum
git commit -m "chore: add cfst-agent skeleton with ip2region dependency"
```

---

### Task 2: 导出 task 包中的必要辅助函数

**Files:**
- Modify: `task/ip.go` — 导出 `IsIPv4`
- Modify: `task/download.go` — 导出 `GetDialContext`

- [ ] **Step 1: 导出 isIPv4 → IsIPv4**

在 `task/ip.go:28-30`，将 `isIPv4` 重命名为 `IsIPv4`：

```go
func IsIPv4(ip string) bool {
	return strings.Contains(ip, ".")
}
```

- [ ] **Step 2: 更新 task 包内部对 isIPv4 的调用**

`task/ip.go` 内部所有 `isIPv4(` 调用改为 `IsIPv4(`：

```bash
# 检查所有引用
grep -rn "isIPv4" task/
```

共 3 处：`ip.go:56`、`ip.go:93`、`ip.go:160`、`ip.go:182`、`download.go:105`、`tcping.go:92`。全部改为 `IsIPv4`。

在 `task/ip.go:56`：
```go
if IsIPv4(ip) {
```

在 `task/ip.go:160`：
```go
if IsIPv4(IP) {
```

在 `task/ip.go:182`：
```go
if IsIPv4(line) {
```

在 `task/download.go:105`：
```go
if IsIPv4(ip.String()) {
```

在 `task/tcping.go:92`：
```go
if IsIPv4(ip.String()) {
```

- [ ] **Step 3: 导出 getDialContext → GetDialContext**

在 `task/download.go:103`，将 `getDialContext` 重命名为 `GetDialContext`，首字母大写即可：

```go
func GetDialContext(ip *net.IPAddr) func(ctx context.Context, network, address string) (net.Conn, error) {
	var fakeSourceAddr string
	if IsIPv4(ip.String()) {
		fakeSourceAddr = fmt.Sprintf("%s:%d", ip.String(), TCPPort)
	} else {
		fakeSourceAddr = fmt.Sprintf("[%s]:%d", ip.String(), TCPPort)
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, fakeSourceAddr)
	}
}
```

- [ ] **Step 4: 更新 task 包内部对 getDialContext 的调用**

`task/download.go:155` 和 `task/httping.go:33` 中 `getDialContext(` 改为 `GetDialContext(`。

- [ ] **Step 5: 验证编译**

```bash
go build ./...
```
Expected: 编译通过

- [ ] **Step 6: Commit**

```bash
git add task/
git commit -m "refactor: export IsIPv4 and GetDialContext from task package"
```

---

### Task 3: agent/evaluate.go — 核心调度器

**Files:**
- Create: `agent/evaluate.go`（覆盖占位文件）

- [ ] **Step 1: 写入完整 evaluate.go**

```go
package agent

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
)

// IPRecord 从原 CFST result.csv 中解析的 IP 数据
type IPRecord struct {
	IP            string
	Sended        int
	Received      int
	LossRate      float64
	AvgDelay      float64 // ms
	DownloadSpeed float64 // MB/s
	Colo          string
}

// DeepResult 深度评估结果
type DeepResult struct {
	IPRecord
	Jitter         float64            // ms (标准差)
	TTFB           float64            // ms
	TCPSuccessRate float64            // 0~1
	Country        string
	City           string
	AIPassed       bool               // AI 端点连通性
	AITTFBs        map[string]float64 // 端点 → TTFB ms
	Score          float64            // 综合得分
	Eliminated     bool               // 被淘汰
	ElimReason     string             // 淘汰原因
}

// Config 评估配置
type Config struct {
	InputCSV      string
	OutputCSV     string
	PrintNum      int
	Concurrency   int
	TCPProbes     int
	TestURL       string
	AIMode        bool
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		InputCSV:    "result.csv",
		OutputCSV:   "result-deep.csv",
		PrintNum:    10,
		Concurrency: 50,
		TCPProbes:   5,
		TestURL:     "https://cf.xiu2.xyz/url",
		AIMode:      false,
	}
}

// parseCSV 解析原 CFST 输出的 CSV 文件
func parseCSV(path string) ([]IPRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开输入文件失败 %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	// 跳过表头
	if _, err := r.Read(); err != nil {
		return nil, fmt.Errorf("读取 CSV 表头失败: %w", err)
	}

	var records []IPRecord
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取 CSV 行失败: %w", err)
		}
		if len(row) < 6 {
			continue
		}
		sended, _ := strconv.Atoi(row[1])
		received, _ := strconv.Atoi(row[2])
		lossRate, _ := strconv.ParseFloat(row[3], 64)
		avgDelay, _ := strconv.ParseFloat(row[4], 64)
		speed, _ := strconv.ParseFloat(row[5], 64)
		colo := ""
		if len(row) >= 7 {
			colo = row[6]
		}
		records = append(records, IPRecord{
			IP:            row[0],
			Sended:        sended,
			Received:      received,
			LossRate:      lossRate,
			AvgDelay:      avgDelay,
			DownloadSpeed: speed,
			Colo:          colo,
		})
	}
	return records, nil
}

// Run 执行深度评估的入口函数
func Run(cfg Config) {
	records, err := parseCSV(cfg.InputCSV)
	if err != nil {
		log.Fatalf("解析输入文件失败: %v", err)
	}
	if len(records) == 0 {
		fmt.Println("[信息] 输入文件中没有 IP 数据，退出。")
		return
	}

	fmt.Printf("共读取 %d 个 IP，开始深度评估...\n", len(records))

	// 初始化各模块
	InitScorer(cfg)
	InitGeoIP()
	defer CloseGeoIP()
	InitAIEndpoints()

	results := make([]DeepResult, len(records))
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Concurrency)

	modeLabel := "通用上网"
	if cfg.AIMode {
		modeLabel = "智能 AI"
	}
	fmt.Printf("评估模式：%s\n\n", modeLabel)

	for i := range records {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[idx] = evaluateOne(records[idx], cfg)
		}(i)
	}
	wg.Wait()

	// 评分
	scoreAll(results)

	// 按得分排序
	sortResults(results)

	// 输出
	PrintResults(results, cfg)
	ExportDeepCSV(results, cfg)
}

// evaluateOne 对单个 IP 执行所有深度评估
func evaluateOne(r IPRecord, cfg Config) DeepResult {
	ip := net.ParseIP(r.IP)
	if ip == nil {
		return DeepResult{IPRecord: r, Eliminated: true, ElimReason: "无效 IP"}
	}
	ipAddr := &net.IPAddr{IP: ip}

	result := DeepResult{IPRecord: r, AITTFBs: make(map[string]float64)}

	// TCP 深度探测
	jitter, avgDelay, successRate := DeepTCPProbe(ipAddr, cfg.TCPProbes)
	result.Jitter = jitter
	if avgDelay > 0 {
		result.AvgDelay = avgDelay // 用深度探测的平均延迟覆盖原值
	}
	result.TCPSuccessRate = successRate

	// TTFB 测量
	result.TTFB = MeasureTTFB(ipAddr, cfg.TestURL)

	// GeoIP 定位
	result.Country, result.City = LookupGeo(ipAddr)

	// AI 端点测试（仅 AI 模式）
	if cfg.AIMode {
		result.AIPassed, result.AITTFBs = TestAIEndpoints(ipAddr)
	}

	// AI 模式硬淘汰
	if cfg.AIMode {
		if r.LossRate > 0.01 {
			result.Eliminated = true
			result.ElimReason = fmt.Sprintf("丢包率 %.2f%% > 1%%", r.LossRate*100)
			return result
		}
		if successRate < 0.8 {
			result.Eliminated = true
			result.ElimReason = fmt.Sprintf("TCP 成功率 %.0f%% < 80%%", successRate*100)
			return result
		}
		if !result.AIPassed {
			result.Eliminated = true
			result.ElimReason = "AI 端点连通性测试未通过"
			return result
		}
	}

	return result
}
```

- [ ] **Step 2: 由于其他模块尚未实现，暂不编译。Commit**

```bash
git add agent/evaluate.go
git commit -m "feat: add evaluate.go with core scheduler and types"
```

---

### Task 4: agent/tcpdeep.go — TCP 深度探测

**Files:**
- Create: `agent/tcpdeep.go`（覆盖占位文件）

- [ ] **Step 1: 写入完整 tcpdeep.go**

```go
package agent

import (
	"fmt"
	"math"
	"net"
	"time"
)

const tcpDeepTimeout = 2 * time.Second

// DeepTCPProbe 对目标 IP 的 443 端口做多次 TCP 连接探测
// 返回: 抖动率(标准差, ms), 平均延迟(ms), 成功率(0~1)
func DeepTCPProbe(ip *net.IPAddr, times int) (jitter, avgDelay float64, successRate float64) {
	var totalDelay float64
	var delays []float64
	success := 0

	var addr string
	if ip.IP.To4() != nil {
		addr = fmt.Sprintf("%s:443", ip.String())
	} else {
		addr = fmt.Sprintf("[%s]:443", ip.String())
	}

	for i := 0; i < times; i++ {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", addr, tcpDeepTimeout)
		if err == nil {
			d := float64(time.Since(start).Milliseconds())
			delays = append(delays, d)
			totalDelay += d
			success++
			conn.Close()
		}
	}

	if success == 0 {
		return 0, 0, 0
	}

	avgDelay = totalDelay / float64(success)
	successRate = float64(success) / float64(times)

	// 计算标准差（抖动率）
	var sumSquares float64
	for _, d := range delays {
		sumSquares += (d - avgDelay) * (d - avgDelay)
	}
	jitter = math.Sqrt(sumSquares / float64(success))

	return
}
```

- [ ] **Step 2: 验证编译**

```bash
go build ./agent/...
```
Expected: 编译通过（evaluate.go 引用的函数已定义）

- [ ] **Step 3: Commit**

```bash
git add agent/tcpdeep.go
git commit -m "feat: add TCP deep probe with jitter calculation"
```

---

### Task 5: agent/ttfbtest.go — HTTP TTFB 测量

**Files:**
- Create: `agent/ttfbtest.go`（覆盖占位文件）

- [ ] **Step 1: 写入 ttfbtest.go**

```go
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
```

- [ ] **Step 2: 验证编译**

```bash
go build ./agent/...
```
Expected: 编译通过

- [ ] **Step 3: Commit**

```bash
git add agent/ttfbtest.go
git commit -m "feat: add HTTP TTFB measurement"
```

---

### Task 6: agent/geoip.go — GeoIP 定位 + 限速器

**Files:**
- Create: `agent/geoip.go`（覆盖占位文件）

- [ ] **Step 1: 写入 geoip.go**

```go
package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/XIU2/CloudflareSpeedTest/data"
	"github.com/lionsoul2014/ip2region/v2/xdb"
)

var (
	geoIDSearcher *xdb.Searcher
	rateLimiter   *tokenBucket
)

// InitGeoIP 初始化 GeoIP 模块：从内嵌数据创建 ip2region 搜索器
func InitGeoIP() {
	var err error
	geoIDSearcher, err = xdb.NewWithBuffer(data.IP2RegionDB)
	if err != nil {
		log.Printf("[警告] ip2region 初始化失败: %v，将仅使用在线 API", err)
	}
	rateLimiter = newTokenBucket(0.75, 5) // 每分钟 45 次 = 每秒 0.75 token, burst 5
}

// CloseGeoIP 释放 GeoIP 资源
func CloseGeoIP() {
	if geoIDSearcher != nil {
		geoIDSearcher.Close()
	}
}

// LookupGeo 查询 IP 的国家和城市
// 优先本地 ip2region，失败则走在线 API
func LookupGeo(ip *net.IPAddr) (country, city string) {
	ipStr := ip.String()

	// 先查本地
	if geoIDSearcher != nil {
		region, err := geoIDSearcher.SearchByStr(ipStr)
		if err == nil && region != "" {
			parts := strings.Split(region, "|")
			if len(parts) >= 5 {
				// 格式: 国家|区域|省份|城市|ISP
				country = parts[0]
				city = parts[3]
				if country == "0" {
					country = ""
				}
				if city == "0" {
					city = ""
				}
				if country != "" {
					return
				}
			}
		}
	}

	// 本地查不到，走在线 API
	return onlineGeoLookup(ipStr)
}

// onlineGeoLookup 通过 ip-api.com 查询（免费，每分钟 45 次上限）
func onlineGeoLookup(ip string) (country, city string) {
	rateLimiter.wait()

	url := fmt.Sprintf("http://ip-api.com/json/%s?fields=countryCode,city", ip)
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()

	var result struct {
		CountryCode string `json:"countryCode"`
		City        string `json:"city"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", ""
	}
	return result.CountryCode, result.City
}

// tokenBucket 简易令牌桶限速器
type tokenBucket struct {
	rate       float64   // 每秒生成的 token 数
	burst      float64   // 最大 burst
	tokens     float64   // 当前令牌数
	lastRefill time.Time // 上次补充时间
	mu         sync.Mutex
}

func newTokenBucket(rate float64, burst int) *tokenBucket {
	return &tokenBucket{
		rate:       rate,
		burst:      float64(burst),
		tokens:     float64(burst),
		lastRefill: time.Now(),
	}
}

func (tb *tokenBucket) wait() {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	// 补充令牌
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens += elapsed * tb.rate
	if tb.tokens > tb.burst {
		tb.tokens = tb.burst
	}
	tb.lastRefill = now

	// 如果令牌不足，计算等待时间
	if tb.tokens < 1 {
		waitTime := time.Duration((1 - tb.tokens) / tb.rate * float64(time.Second))
		tb.mu.Unlock()
		time.Sleep(waitTime)
		tb.mu.Lock()
		tb.tokens = 0
	} else {
		tb.tokens--
	}
}
```

- [ ] **Step 2: 验证编译**

```bash
go build ./agent/...
```
Expected: 编译通过

- [ ] **Step 3: Commit**

```bash
git add agent/geoip.go
git commit -m "feat: add GeoIP lookup with ip2region + online fallback + rate limiter"
```

---

### Task 7: agent/ai.go — AI 端点测试

**Files:**
- Create: `agent/ai.go`（覆盖占位文件）

- [ ] **Step 1: 写入 ai.go**

```go
package agent

import (
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

// InitAIEndpoints 初始化 AI 端点列表
func InitAIEndpoints() {}

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
			passed, ttfb := testSingleEndpoint(ip, ep)
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

// testSingleEndpoint 测试单个 AI 端点
func testSingleEndpoint(ip *net.IPAddr, endpoint string) (passed bool, ttfb float64) {
	// TCP 连通性测试
	conn, err := net.DialTimeout("tcp", endpoint, 2*time.Second)
	if err != nil {
		return false, 0
	}
	conn.Close()
	passed = true

	// HTTPS TTFB 测试
	host, _, _ := net.SplitHostPort(endpoint)
	url := fmt.Sprintf("https://%s/", host)

	dialer := func(network, addr string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).Dial(network, endpoint)
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return dialer("tcp", endpoint)
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
```

等等，上面 `testSingleEndpoint` 用了 `context` 但没导入。修正：

```go
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

// InitAIEndpoints 初始化 AI 端点列表
func InitAIEndpoints() {}

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
			passed, ttfb := testSingleEndpoint(ip, ep)
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
func testSingleEndpoint(ip *net.IPAddr, endpoint string) (passed bool, ttfb float64) {
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
		return true, 0
	}
	ttfb = float64(time.Since(start).Milliseconds())
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	return true, ttfb
}
```

- [ ] **Step 2: 验证编译**

```bash
go build ./agent/...
```
Expected: 编译通过

- [ ] **Step 3: Commit**

```bash
git add agent/ai.go
git commit -m "feat: add AI endpoint connectivity and TTFB tests"
```

---

### Task 8: agent/scorer.go — 评分引擎

**Files:**
- Create: `agent/scorer.go`（覆盖占位文件）

- [ ] **Step 1: 写入 scorer.go**

```go
package agent

import (
	"sort"
)

// AsiaPacific 亚太国家代码
var AsiaPacific = map[string]bool{
	"JP": true, "KR": true, "SG": true, "HK": true,
	"TW": true, "MO": true, "TH": true, "VN": true,
	"MY": true, "IN": true, "ID": true, "PH": true,
	"AU": true, "NZ": true, "CN": true,
}

// EuropeAmerica 欧美国家代码
var EuropeAmerica = map[string]bool{
	"US": true, "CA": true, "GB": true, "DE": true,
	"FR": true, "NL": true, "SE": true, "CH": true,
	"IT": true, "ES": true, "PT": true, "IE": true,
	"BE": true, "AT": true, "DK": true, "FI": true,
	"NO": true, "LU": true, "PL": true, "CZ": true,
}

var currentCfg Config

// InitScorer 保存配置供评分使用
func InitScorer(cfg Config) {
	currentCfg = cfg
}

// scoreDimension 单维度百分制打分
// value < 50ms → 100, value > 500ms → 0, 线性折算
func scoreDimension(valueMs float64) float64 {
	if valueMs <= 0 {
		return 0
	}
	if valueMs <= 50 {
		return 100
	}
	if valueMs >= 500 {
		return 0
	}
	return (500 - valueMs) / (500 - 50) * 100
}

// geoScore 地理位置分
func geoScore(country string, preferAsia bool) float64 {
	if country == "" {
		return 0
	}
	if preferAsia {
		if AsiaPacific[country] {
			return 100
		}
		if EuropeAmerica[country] {
			return 30
		}
		return 0
	}
	// AI 模式：优先欧美
	if EuropeAmerica[country] {
		return 100
	}
	if AsiaPacific[country] {
		return 30
	}
	return 0
}

// aiBonus AI 接口加分：每个端点 TTFB < 200ms 加 3 分，全通过额外 +1（封顶 10）
func aiBonus(ttfbs map[string]float64) float64 {
	if len(ttfbs) == 0 {
		return 0
	}
	bonus := 0.0
	allFast := true
	for _, ttfb := range ttfbs {
		if ttfb > 0 && ttfb < 200 {
			bonus += 3
		} else {
			allFast = false
		}
	}
	if allFast && len(ttfbs) == 3 {
		bonus += 1
	}
	if bonus > 10 {
		bonus = 10
	}
	return bonus
}

// scoreOne 对单个结果打分
func scoreOne(r *DeepResult) {
	if r.Eliminated {
		r.Score = 0
		return
	}

	if currentCfg.AIMode {
		// AI 模式权重
		delayScore := scoreDimension(r.AvgDelay) * 0.35
		jitterScore := scoreDimension(r.Jitter) * 0.30
		ttfbScore := scoreDimension(r.TTFB) * 0.25
		speedScore := scoreDimension(r.DownloadSpeed*1024*1024/10) * 0.05 // 归一化下载速度
		geoS := geoScore(r.Country, false) * 0.05
		bonus := aiBonus(r.AITTFBs)
		r.Score = delayScore + jitterScore + ttfbScore + speedScore + geoS + bonus
	} else {
		// 通用模式权重
		speedScore := scoreDimension(r.DownloadSpeed*1024*1024/10) * 0.40

		// 下载速度归一化：1MB/s≈100KB/s 作为参考点
		// 实际上用另一种方式：下载速度越大分数越高
		speedScore = 0
		if r.DownloadSpeed > 0 {
			// MB/s → 分数：10 MB/s = 100分
			speedScore = (r.DownloadSpeed / 10.0) * 100
			if speedScore > 100 {
				speedScore = 100
			}
		}
		speedScore *= 0.40

		delayScore := scoreDimension(r.AvgDelay) * 0.25
		jitterScore := scoreDimension(r.Jitter) * 0.10
		ttfbScore := scoreDimension(r.TTFB) * 0.15
		geoS := geoScore(r.Country, true) * 0.10

		r.Score = speedScore + delayScore + jitterScore + ttfbScore + geoS
	}
}

// scoreAll 对全部结果打分
func scoreAll(results []DeepResult) {
	for i := range results {
		scoreOne(&results[i])
	}
}

// sortResults 按得分降序排列，淘汰的排最后
func sortResults(results []DeepResult) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Eliminated != results[j].Eliminated {
			return !results[i].Eliminated
		}
		return results[i].Score > results[j].Score
	})
}
```

- [ ] **Step 2: 验证编译**

```bash
go build ./agent/...
```
Expected: 编译通过

- [ ] **Step 3: Commit**

```bash
git add agent/scorer.go
git commit -m "feat: add scoring engine with dual modes and absolute scoring"
```

---

### Task 9: agent/output.go — 结果输出

**Files:**
- Create: `agent/output.go`（覆盖占位文件）

- [ ] **Step 1: 写入 output.go**

```go
package agent

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
)

// PrintResults 在终端打印评估结果
func PrintResults(results []DeepResult, cfg Config) {
	if cfg.PrintNum <= 0 {
		return
	}
	if len(results) == 0 {
		fmt.Println("[信息] 结果为空，跳过输出。")
		return
	}
	n := cfg.PrintNum
	if n > len(results) {
		n = len(results)
	}

	headFmt := "%-18s%-8s%-10s%-10s%-10s%-10s%-10s%-8s%-10s\n"
	dataFmt := "%-18s%-8.1f%-10.2f%-10.2f%-10s%-10s%-10.2f%-8s%-10s\n"

	// 如果包含 IPv6 地址，加大列宽
	for i := 0; i < n; i++ {
		if len(results[i].IP) > 18 {
			headFmt = "%-42s%-8s%-10s%-10s%-10s%-10s%-10s%-8s%-10s\n"
			dataFmt = "%-42s%-8.1f%-10.2f%-10.2f%-10s%-10s%-10.2f%-8s%-10s\n"
			break
		}
	}

	fmt.Println()
	fmt.Printf(headFmt, "IP 地址", "得分", "延迟(ms)", "抖动(ms)", "TTFB(ms)", "国家", "城市", "下载(MB/s)", "状态")
	fmt.Println("----------------------------------------------------------------------------------------------------------")

	for i := 0; i < n; i++ {
		r := results[i]
		status := "✓"
		if r.Eliminated {
			status = "✗ " + r.ElimReason
		}
		countryCity := r.Country
		if r.City != "" {
			countryCity = r.Country + "/" + r.City
		}
		fmt.Printf(dataFmt,
			r.IP, r.Score, r.AvgDelay, r.Jitter, r.TTFB,
			r.Country, r.City,
			r.DownloadSpeed, status,
		)
	}

	if cfg.OutputCSV != "" && cfg.OutputCSV != " " {
		fmt.Printf("\n完整评估结果已写入 %s\n", cfg.OutputCSV)
	}
}

// ExportDeepCSV 将完整结果写入 CSV
func ExportDeepCSV(results []DeepResult, cfg Config) {
	if cfg.OutputCSV == "" || cfg.OutputCSV == " " {
		return
	}
	if len(results) == 0 {
		return
	}

	f, err := os.Create(cfg.OutputCSV)
	if err != nil {
		log.Printf("创建输出文件失败: %v", err)
		return
	}
	defer f.Close()

	w := csv.NewWriter(f)
	_ = w.Write([]string{"IP 地址", "综合得分", "平均延迟", "抖动率", "TTFB", "下载速度(MB/s)", "TCP成功率", "国家", "城市", "AI端点通过", "淘汰", "淘汰原因"})

	for _, r := range results {
		aiPassed := "N/A"
		if cfg.AIMode {
			aiPassed = strconv.FormatBool(r.AIPassed)
		}
		eliminated := strconv.FormatBool(r.Eliminated)
		row := []string{
			r.IP,
			strconv.FormatFloat(r.Score, 'f', 1, 64),
			strconv.FormatFloat(r.AvgDelay, 'f', 2, 64),
			strconv.FormatFloat(r.Jitter, 'f', 2, 64),
			strconv.FormatFloat(r.TTFB, 'f', 2, 64),
			strconv.FormatFloat(r.DownloadSpeed, 'f', 2, 64),
			strconv.FormatFloat(r.TCPSuccessRate, 'f', 2, 64),
			r.Country,
			r.City,
			aiPassed,
			eliminated,
			r.ElimReason,
		}
		_ = w.Write(row)
	}
	w.Flush()
}
```

等等，`output.go` 中引用了 `sort` 但实际没用到（sortResults 在 scorer.go 中）。去掉 import 中的 `sort`。

修正 import：
```go
import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"strconv"
)
```

- [ ] **Step 2: 验证编译**

```bash
go build ./agent/...
```
Expected: 编译通过

- [ ] **Step 3: Commit**

```bash
git add agent/output.go
git commit -m "feat: add result printing and CSV export"
```

---

### Task 10: cmd/agent/main.go — CLI 入口

**Files:**
- Create: `cmd/agent/main.go`（覆盖占位文件）

- [ ] **Step 1: 写入完整 main.go**

```go
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/XIU2/CloudflareSpeedTest/agent"
)

var version = "v1.0.0"

func main() {
	cfg := agent.DefaultConfig()

	flag.StringVar(&cfg.InputCSV, "i", cfg.InputCSV, "输入文件（CFST 输出的 result.csv）")
	flag.StringVar(&cfg.OutputCSV, "o", cfg.OutputCSV, "输出 CSV 文件路径，\"\" 不输出")
	flag.IntVar(&cfg.PrintNum, "p", cfg.PrintNum, "终端显示结果数量，0 不显示")
	flag.IntVar(&cfg.Concurrency, "n", cfg.Concurrency, "并发数")
	flag.IntVar(&cfg.TCPProbes, "t", cfg.TCPProbes, "TCP 探测次数")
	flag.StringVar(&cfg.TestURL, "url", cfg.TestURL, "TTFB 测试地址")
	flag.BoolVar(&cfg.AIMode, "ai", cfg.AIMode, "AI 模式")

	printVersion := flag.Bool("v", false, "打印版本")

	flag.Usage = func() {
		fmt.Printf(`CFST-Agent %s
CloudflareSpeedTest 二阶段 IP 深度评估工具
https://github.com/XIU2/CloudflareSpeedTest

参数：
    -i      输入文件（默认 result.csv）
    -o      输出 CSV 文件
    -p      终端显示结果数量（默认 10）
    -n      并发数（默认 50）
    -t      TCP 探测次数（默认 5）
    -url    TTFB 测试地址
    -ai     AI 模式
    -v      打印版本
`, version)
	}
	flag.Parse()

	if *printVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	agent.Run(cfg)

	if runtime.GOOS == "windows" && cfg.PrintNum > 0 {
		fmt.Print("按下 回车键 或 Ctrl+C 退出。")
		fmt.Scanln()
	}
}
```

- [ ] **Step 2: 验证编译整个项目**

```bash
go build ./...
```
Expected: 编译通过

- [ ] **Step 3: 编译 cfst-agent 二进制**

```bash
cd cmd/agent && go build -o ../../cfst-agent -ldflags "-s -w -X main.version=v1.0.0"
```
Expected: 在项目根目录生成 `cfst-agent.exe`（Windows）

- [ ] **Step 4: Commit**

```bash
git add cmd/agent/main.go
git commit -m "feat: add cfst-agent CLI entry point"
```

---

### Task 11: 集成验证与修复

**Files:**
- Modify: 可能需要修复编译/链接问题

- [ ] **Step 1: 全量编译**

```bash
go build ./...
go vet ./...
```
Expected: 无错误

- [ ] **Step 2: 用样本 CSV 做烟雾测试**

创建 `test_sample.csv`：
```
IP 地址,已发送,已接收,丢包率,平均延迟,下载速度(MB/s),地区码
104.27.200.69,4,4,0.00,146.23,28.64,LAX
172.67.60.78,4,4,0.00,139.82,15.02,SEA
```

运行：
```bash
./cfst-agent -i test_sample.csv -p 5
```
Expected: 程序运行完成，输出 5 条带评分的 IP 结果

- [ ] **Step 3: 测试 --ai 模式**

```bash
./cfst-agent -i test_sample.csv -p 5 --ai
```
Expected: AI 模式运行完成

- [ ] **Step 4: 清理测试文件并 Commit**

```bash
rm -f test_sample.csv
git add -A
git commit -m "test: verify cfst-agent integration smoke test"
```

---

### 计划自审

1. **Spec 覆盖检查：**
   - CSV 输入解析 → Task 3 `parseCSV()`
   - TCP 深度探测 + 抖动率 → Task 4 `DeepTCPProbe()`
   - HTTP TTFB 测量 → Task 5 `MeasureTTFB()`
   - GeoIP 定位（ip2region + API 兜底 + 限速） → Task 6
   - AI 端点测试（TCP 连通 + HTTPS TTFB） → Task 7
   - 两套评分模式 → Task 8 `scoreOne()`
   - 绝对百分制打分 → Task 8 `scoreDimension()`
   - 淘汰规则 → Task 3 `evaluateOne()` 中判断
   - 终端输出 + CSV 导出 → Task 9
   - CLI 入口 + 参数 → Task 10
   - 编译构建 → Task 11

2. **占位符检查：** 无 TBD/TODO

3. **类型一致性：** `IPRecord` / `DeepResult` / `Config` 在各文件中引用一致。`scoreAll` / `sortResults` / `PrintResults` / `ExportDeepCSV` 均为 `agent` 包内函数。
