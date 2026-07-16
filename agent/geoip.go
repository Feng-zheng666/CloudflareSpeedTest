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
	tb.mu.Unlock()
}
