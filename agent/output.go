package agent

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
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
	dataFmt := "%-18s%-8.1f%-10.2f%-10.2f%-10.2f%-10s%-10s%-8.2f%-10s\n"

	// 如果包含 IPv6 地址，加大列宽
	for i := 0; i < n; i++ {
		if len(results[i].IP) > 18 {
			headFmt = "%-42s%-8s%-10s%-10s%-10s%-10s%-10s%-8s%-10s\n"
			dataFmt = "%-42s%-8.1f%-10.2f%-10.2f%-10.2f%-10s%-10s%-8.2f%-10s\n"
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
