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
	InputCSV    string
	OutputCSV   string
	PrintNum    int
	Concurrency int
	TCPProbes   int
	TestURL     string
	AIMode      bool
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
