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
