package agent

import "sort"

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
		delayScore := scoreDimension(r.AvgDelay) * 0.35
		jitterScore := scoreDimension(r.Jitter) * 0.30
		ttfbScore := scoreDimension(r.TTFB) * 0.25
		speedScore := scoreDimension(r.DownloadSpeed*1024*1024/10) * 0.05
		geoS := geoScore(r.Country, false) * 0.05
		bonus := aiBonus(r.AITTFBs)
		r.Score = delayScore + jitterScore + ttfbScore + speedScore + geoS + bonus
	} else {
		// 下载速度分：10 MB/s = 100分
		var speedScore float64
		if r.DownloadSpeed > 0 {
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
