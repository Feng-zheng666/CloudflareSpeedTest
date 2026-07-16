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
