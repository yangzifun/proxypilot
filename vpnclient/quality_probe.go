package main

import (
	"context"
	"log"
	"net"
	"time"
)

// 隧道质量采样：连接建立后，每隔 2 秒对节点网关发起一次真实 DNS 查询，
// 统计丢包率与延迟分布。
//
// 为什么要它：Windows 的 ping 只反映「用户看到的现象」，分不清到底是
//   · 隧道里真的丢包（队列溢出 / QUIC 丢包）
//   · 还是只是延迟超过 1 秒被判超时（队头阻塞 / 重传退避）
// 这个采样直接测隧道内部往返，并且把 QUIC 的丢包计数一起暴露出来，
// 两者一对比就能定性。

const (
	qualityInterval = 2 * time.Second
	qualityTimeout  = 1500 * time.Millisecond
	// 连续失败到这个次数才判定「不通」，避免单次抖动就翻脸
	qualityFailThreshold = 3
)

// runQualityProbe 持续采样，结果写进 sessionState。
func (s *uiServer) runQualityProbe(ctx context.Context, attempt int64, localIP, gateway string) {
	target := net.JoinHostPort(gateway, "53")
	if gateway == "" {
		log.Printf("质量采样：没有网关地址，跳过")
		return
	}
	log.Printf("质量采样：开始（目标 %s，每 %s 一次）", target, qualityInterval)

	ticker := time.NewTicker(qualityInterval)
	defer ticker.Stop()
	consecutiveFail := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		rtt, err := probeTunnelOnce(localIP, target, qualityTimeout)
		ok := err == nil

		var info string
		if ok {
			consecutiveFail = 0
			info = formatQualityInfo(rtt)
		} else {
			consecutiveFail++
			info = err.Error()
		}

		s.setFor(attempt, func(st *sessionState) {
			st.qSent++
			if ok {
				st.qOK++
				st.qLastRTT = int(rtt.Milliseconds())
				if st.qLastRTT > st.qMaxRTT {
					st.qMaxRTT = st.qLastRTT
				}
			} else {
				st.qLost++
			}
			st.qConsecutiveFail = consecutiveFail
			// 连续失败够多才认定数据面异常
			if consecutiveFail >= qualityFailThreshold {
				st.netOK = false
				st.netErr = info
				st.probeOK = false
				st.probeInfo = "隧道质量采样连续失败"
			} else if ok {
				st.netOK = true
				st.netRTT = int(rtt.Milliseconds())
				st.netErr = ""
				st.probeOK = true
				st.probeInfo = info
			}
			st.refreshTunnelStats()
		})
	}
}

func formatQualityInfo(rtt time.Duration) string {
	return "隧道往返 " + rtt.Round(time.Millisecond).String()
}