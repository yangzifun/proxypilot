const fs = require('fs');
const p = 'server/internal/api/agent_poll.go';
let t = fs.readFileSync(p, 'utf8').replace(/\r\n/g, '\n');
const before = t;

// 1) 解析 agent 上报的 idle/steal
t = t.replace(
`	m.CpuTicks = jsonInt(body["cpu_ticks"])`,
`	m.CpuTicks = jsonInt(body["cpu_ticks"])
	m.CpuIdleTicks = jsonInt(body["cpu_idle_ticks"])
	m.CpuStealTicks = jsonInt(body["cpu_steal_ticks"])`
);

// 2) 结构体字段
t = t.replace(
`	CpuTicks                      int64`,
`	CpuTicks                      int64
	CpuIdleTicks                  int64
	CpuStealTicks                 int64`
);

// 3) CPU 计算：扣除 steal，用 busy/(busy+idle) 才是本机真实占用
t = t.replace(
`func agentCpuPercentFromSample(prev *prevSample, m *agentMetrics, intervalSec int64) float64 {
	if prev == nil || prev.Raw == nil {
		return 0
	}
	prevTicks := int64(jsonNum(prev.Raw["cpuTicks"]))
	if prevTicks > 0 && m.CpuTicks > prevTicks {
		seconds := float64(time.Now().UnixMilli()-prev.CreatedAt) / 1000
		if seconds < 1 {
			seconds = float64(intervalSec)
		}
		if seconds < 1 {
			seconds = 60
		}
		cores := m.Cores
		if cores < 1 {
			cores = 1
		}
		pct := (float64(m.CpuTicks-prevTicks) / (float64(cores) * seconds * 100)) * 1000
		return math.Min(100, math.Round(pct)/10)
	}
	return 0
}`,
`func agentCpuPercentFromSample(prev *prevSample, m *agentMetrics, intervalSec int64) float64 {
	if prev == nil || prev.Raw == nil {
		return 0
	}
	prevTicks := int64(jsonNum(prev.Raw["cpuTicks"]))
	if prevTicks <= 0 || m.CpuTicks <= prevTicks {
		return 0
	}
	seconds := float64(time.Now().UnixMilli()-prev.CreatedAt) / 1000
	if seconds < 1 {
		seconds = float64(intervalSec)
	}
	if seconds < 1 {
		seconds = 60
	}
	cores := m.Cores
	if cores < 1 {
		cores = 1
	}

	dBusy := float64(m.CpuTicks - prevTicks)
	prevIdle := int64(jsonNum(prev.Raw["cpuIdleTicks"]))
	prevSteal := int64(jsonNum(prev.Raw["cpuStealTicks"]))

	// 新版本 Agent 会同时上报 idle 与 steal：
	// 云主机 steal 很高时，总时间几乎都不是 idle，直接用 busy/总时间 会长期报 100%。
	// 用 busy/(busy+idle) 才是「这台机器自己用掉的比例」。
	if m.CpuIdleTicks > 0 || m.CpuStealTicks > 0 {
		dIdle := float64(m.CpuIdleTicks - prevIdle)
		if dIdle < 0 {
			dIdle = 0
		}
		denom := dBusy + dIdle
		if denom > 0 {
			return math.Min(100, math.Round(dBusy/denom*1000)/10)
		}
		return 0
	}

	// 老版本 Agent（无 idle/steal）：沿用按核数×时间折算
	pct := (dBusy / (float64(cores) * seconds * 100)) * 1000
	return math.Min(100, math.Round(pct)/10)
}

// agentStealPercentFromSample 计算宿主机抢占比例（用于界面提示）。
func agentStealPercentFromSample(prev *prevSample, m *agentMetrics) float64 {
	if prev == nil || prev.Raw == nil || m.CpuStealTicks <= 0 {
		return 0
	}
	prevBusy := int64(jsonNum(prev.Raw["cpuTicks"]))
	prevIdle := int64(jsonNum(prev.Raw["cpuIdleTicks"]))
	prevSteal := int64(jsonNum(prev.Raw["cpuStealTicks"]))
	dBusy := float64(m.CpuTicks - prevBusy)
	dIdle := float64(m.CpuIdleTicks - prevIdle)
	dSteal := float64(m.CpuStealTicks - prevSteal)
	if dBusy < 0 || dIdle < 0 || dSteal <= 0 {
		return 0
	}
	total := dBusy + dIdle + dSteal
	if total <= 0 {
		return 0
	}
	return math.Min(100, math.Round(dSteal/total*1000)/10)
}`);

if (t === before) throw new Error('no change');
fs.writeFileSync(p, t, 'utf8');
console.log('idle parse:', t.includes('cpu_idle_ticks'), 'steal fn:', t.includes('agentStealPercentFromSample'));