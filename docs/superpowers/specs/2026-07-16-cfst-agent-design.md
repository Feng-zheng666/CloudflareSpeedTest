# CFST-Agent 设计文档

> 日期: 2026-07-16 | 状态: 已确认

## 概述

在现有 CloudflareSpeedTest 基础上新增 `cfst-agent`，直接修改的二阶段 IP 深度评估工具。
原 `cfst` 做海选（下载速度第一），`cfst-agent` 做深度筛选（稳定性/延迟/地理/抖动综合评估）。

## 项目结构

```
CloudflareSpeedTest/
├── main.go                     # 原 cfst 入口（不动）
├── task/                       # 原测速逻辑（不动）
├── utils/                      # 原工具包（不动）
├── cmd/agent/main.go           # cfst-agent 入口
├── agent/
│   ├── evaluate.go             # 核心调度：读 CSV → 深度评估 → 打分 → 输出
│   ├── tcpdeep.go              # TCP 多探针 + 抖动率（标准差）
│   ├── ttfbtest.go             # HTTP TTFB 测量
│   ├── geoip.go                # ip2region 本地库 + 在线 API 兜底 + 限速器
│   ├── ai.go                   # AI 端点连通性测试
│   ├── scorer.go               # 评分引擎（通用模式 / AI 模式）
│   └── output.go               # 结果排序、终端打印、CSV 导出
├── data/
│   └── ip2region.xdb            # ip2region 数据库文件（嵌入二进制或外置）
```

## 数据流

```
result.csv → [CSV 解析] → []IPRecord
        ↓
[并发三路评估] ├─ tcpdeep  → 抖动率 (jitter)
              ├─ ttfbtest → TTFB (time to first byte)
              ├─ geoip    → 国家/城市 (country/city)
              └─ ai       → 仅 --ai 模式, TCP 连通性 + HTTPS TTFB
        ↓
[评分引擎] → 1. AI 模式硬淘汰: 丢包率 > 1% 或 TCP 成功率 < 80% → 直接淘汰
             2. 百分制打分: 各维度独立评分 (50ms=100分, 500ms=0分, 线性折算)
             3. 模式权重加权求和
             4. AI 端点加分项 (HTTPS TTFB 达标则加分)
        ↓
[排序 → 终端打印 → CSV 导出]
```

## 模块说明

### evaluate.go — 调度器
- 读取 `-i` 指定的 CSV 文件（原 CFST 输出格式）
- 解析为 IPRecord 列表，每项包含：IP、已发送、已接收、丢包率、平均延迟、下载速度、地区码
- 用 `-n` 控制的 goroutine 并发池执行三路评估
- 收集结果后调用 scorer 打分、output 输出

### tcpdeep.go — TCP 深度探测
- 对每个 IP 的 443 端口做 `-t` 次（默认 5 次）TCP Dial
- 记录每次连接耗时
- 计算出：平均延迟、标准差（抖动率 jitter）、成功率

### ttfbtest.go — HTTP TTFB 测量
- 通过自定义 DialContext 绑定被测 IP
- 对 `-url` 指定地址发起 HTTPS GET
- 记录 TTFB：从发送请求到收到 Response 第一个字节的时间
- 不下载完整 body，读到首个字节后即关闭连接

### geoip.go — GeoIP 定位
- 优先 ip2region 本地 `.xdb` 文件查询 → 获取国家、城市
- 本地查不到（或结果为空）时走 ip-api.com 在线查询
- 在线 API 限速：token bucket 每分钟 45 次，排队等待
- ip2region.xdb 文件通过 Go embed 嵌入二进制

### ai.go — AI 端点测试
- 仅在 `--ai` 模式下执行
- 对三个核心端点各做 1 次 TCP 连接测试（超时 2s）：`api.openai.com:443`、`api.anthropic.com:443`、`gateway.ai.cloudflare.com:443`
- 任一不通 → 该 IP 标记淘汰（硬淘汰，不进评分）
- 对连通的端点做 HTTPS TTFB 测试 → 记录各端点 TTFB
- 返回：是否通过淘汰（bool）、各端点 TTFB 列表

### scorer.go — 评分引擎

**单维度百分制公式**（适用于延迟、抖动、TTFB）：
```
score = (500 - value) / (500 - 50) × 100
clamp: < 50ms → 100, > 500ms → 0
```

**通用模式权重**：

| 维度 | 权重 |
|------|------|
| 下载速度 | 40% |
| 平均延迟 | 25% |
| 抖动率 | 10% |
| TTFB | 15% |
| 地理位置（亚太）| 10% |

地理位置分：亚太国家(JP/KR/SG/HK/TW/MO/TH/VN/MY/IN/ID/PH/AU) → 100，欧美 → 30，其他 → 0

**AI 模式权重**：

| 维度 | 权重 |
|------|------|
| 平均延迟 | 35% |
| 抖动率 | 30% |
| TTFB | 25% |
| 下载速度 | 5% |
| 地理位置（欧美）| 5% |
| AI 接口加分 | +10 封顶 |

地理位置分：欧美国家(US/CA/GB/DE/FR/NL/SE/CH/IT/ES/PT/IE/BE/AT/DK/FI/NO) → 100，亚太 → 30，其他 → 0

AI 接口加分：3 个端点各 TTFB < 200ms 加 3 分，全通过额外 +1

### output.go — 输出
- 按综合得分降序排列
- 终端输出前 `-p` 条结果：IP、得分、国家、城市、延迟/抖动/TTFB、下载速度
- 写入 `-o` 指定的 CSV 文件（完整结果）

## 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-i` | `result.csv` | 输入文件（CFST 输出） |
| `-o` | `result-deep.csv` | 输出 CSV 文件 |
| `-p` | `10` | 终端显示结果数量 |
| `-n` | `50` | 并发数 |
| `-t` | `5` | TCP 探测次数 |
| `-url` | `https://cf.xiu2.xyz/url` | TTFB 测试地址 |
| `--ai` | `false` | AI 模式开关 |

## 淘汰规则

| 条件 | 通用模式 | AI 模式 |
|------|---------|--------|
| 丢包率 > 1% | 不淘汰 | 淘汰 |
| TCP 成功率 < 80% | 不淘汰 | 淘汰 |
| AI 端点任一不通 | — | 淘汰 |
| 抖动率 > 任意阈值 | 不淘汰 | 不淘汰（只扣分） |

## 依赖

- `github.com/lionsoul2014/ip2region/v2` — 本地 GeoIP 查询（新增）
- 复用现有 `github.com/VividCortex/ewma`、`github.com/cheggaaa/pb/v3`、`github.com/fatih/color`
- Go 1.18+（与现有项目一致）

## 编译

```bash
# 在项目根目录
cd cmd/agent && go build -o ../../cfst-agent -ldflags "-s -w -X main.version=v1.0.0"
```
