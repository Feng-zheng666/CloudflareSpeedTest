package task

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/XIU2/CloudflareSpeedTest/utils"
)

const (
	tcpConnectTimeout = time.Millisecond * 500
	maxRoutine        = 1000
	defaultRoutines   = 200
	defaultPort       = 443
	defaultPingTimes  = 5
)

var (
	Routines      = defaultRoutines
	TCPPort   int = defaultPort
	PingTimes int = defaultPingTimes
)

type Ping struct {
	wg      *sync.WaitGroup
	m       *sync.Mutex
	ips     []*net.IPAddr
	csv     utils.PingDelaySet
	control chan bool
	bar     *utils.Bar
}

func checkPingDefault() {
	if Routines <= 0 {
		Routines = defaultRoutines
	}
	if TCPPort <= 0 || TCPPort >= 65535 {
		TCPPort = defaultPort
	}
	if PingTimes <= 0 {
		PingTimes = defaultPingTimes
	}
}

func NewPing() *Ping {
	checkPingDefault()
	ips := loadIPRanges()
	return &Ping{
		wg:      &sync.WaitGroup{},
		m:       &sync.Mutex{},
		ips:     ips,
		csv:     make(utils.PingDelaySet, 0),
		control: make(chan bool, Routines),
		bar:     utils.NewBar(len(ips), "可用:", ""),
	}
}

func (p *Ping) Run() utils.PingDelaySet {
	if len(p.ips) == 0 {
		return p.csv
	}
	if Httping {
		utils.Cyan.Printf("开始延迟测速（模式：HTTP, 端口：%d, 范围：%v ~ %v ms, 丢包：%.2f)\n", TCPPort, utils.InputMinDelay.Milliseconds(), utils.InputMaxDelay.Milliseconds(), utils.InputMaxLossRate)
	} else {
		utils.Cyan.Printf("开始延迟测速（模式：TCP, 端口：%d, 范围：%v ~ %v ms, 丢包：%.2f)\n", TCPPort, utils.InputMinDelay.Milliseconds(), utils.InputMaxDelay.Milliseconds(), utils.InputMaxLossRate)
	}
	for _, ip := range p.ips {
		p.wg.Add(1)
		p.control <- false
		go p.start(ip)
	}
	p.wg.Wait()
	p.bar.Done()
	sort.Sort(p.csv)
	return p.csv
}

func (p *Ping) start(ip *net.IPAddr) {
	defer p.wg.Done()
	p.tcpingHandler(ip)
	<-p.control
}

// bool connectionSucceed float32 time
func (p *Ping) tcpingSingle(ctx context.Context, ip *net.IPAddr) (bool, time.Duration) {
	startTime := time.Now()
	var fullAddress string
	if IsIPv4(ip.String()) {
		fullAddress = fmt.Sprintf("%s:%d", ip.String(), TCPPort)
	} else {
		fullAddress = fmt.Sprintf("[%s]:%d", ip.String(), TCPPort)
	}
	d := net.Dialer{Timeout: tcpConnectTimeout}
	conn, err := d.DialContext(ctx, "tcp", fullAddress)
	if err != nil {
		return false, 0
	}
	conn.Close()
	return true, time.Since(startTime)
}

// tcpingConcurrent 并发 TCP Ping，首次成功即取消其余探测，大幅提升测速效率
func (p *Ping) tcpingConcurrent(ip *net.IPAddr) (recv int, totalDelay time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), tcpConnectTimeout)
	defer cancel()

	type result struct {
		ok    bool
		delay time.Duration
	}
	results := make(chan result, PingTimes)
	for i := 0; i < PingTimes; i++ {
		go func() {
			ok, delay := p.tcpingSingle(ctx, ip)
			results <- result{ok, delay}
		}()
	}

	// 收到第一个成功结果后立即取消其余 goroutine，不通的 IP 也只需等一次超时
	for i := 0; i < PingTimes; i++ {
		r := <-results
		if r.ok {
			recv++
			totalDelay += r.delay
			if recv == 1 {
				cancel()
			}
		}
	}
	return
}

// pingReceived pingTotalTime
func (p *Ping) checkConnection(ip *net.IPAddr) (recv int, totalDelay time.Duration, jitter time.Duration, colo string) {
	if Httping {
		recv, totalDelay, jitter, colo = p.httping(ip)
		return
	}
	colo = "" // TCPing 不获取 colo
	recv, totalDelay = p.tcpingConcurrent(ip)
	return
}

func (p *Ping) appendIPData(data *utils.PingData) {
	p.m.Lock()
	defer p.m.Unlock()
	p.csv = append(p.csv, utils.CloudflareIPData{
		PingData: data,
	})
}

// handle tcping
func (p *Ping) tcpingHandler(ip *net.IPAddr) {
	recv, totalDlay, jitter, colo := p.checkConnection(ip)
	nowAble := len(p.csv)
	if recv != 0 {
		nowAble++
	}
	p.bar.Grow(1, strconv.Itoa(nowAble))
	if recv == 0 {
		return
	}
	data := &utils.PingData{
		IP:       ip,
		Sended:   PingTimes,
		Received: recv,
		Delay:    totalDlay / time.Duration(recv),
		Jitter:   jitter,
		Colo:     colo,
	}
	p.appendIPData(data)
}
