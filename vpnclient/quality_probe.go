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
	// 采样间隔 5 秒：既要能及时发现断流，又不能给大流量添堵
	qualityInterval = 5 * time.Second
	// 采样超时给到 3 秒：满速下载时采样包会排在大流量后面，
	// 1.5 秒太短，会把「拥塞」误判成「不通」。
	qualityTimeout = 3 * time.Second
	// 连续失败到这个次数才判定「不通」，避免单次抖动就翻脸
	qualityFailThreshold = 5
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
	// 上一次采样的收发字节数，用来判断「隧道里到底有没有流量」
	var lastRx, lastTx uint64

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
			st.refreshTunnelStats()

			// 采样包失败 ≠ 隧道不通：满速传输时它会被大流量挤在后面。
			// 只要隧道里还有字节在走，就说明数据面是通的，
			// 不因为采样超时把状态翻成「不通」。
			flowing := st.rx != lastRx || st.tx != lastTx
			lastRx, lastTx = st.rx, st.tx

			if ok {
				st.netOK = true
				st.netRTT = int(rtt.Milliseconds())
				st.netErr = ""
				st.probeOK = true
				st.probeInfo = info
				return
			}
			if flowing {
				// 有流量：判为「忙」而不是「断」
				st.netOK = true
				st.netErr = ""
				st.probeOK = true
				st.probeInfo = "采样超时，但隧道有流量（可能正被大流量占满）"
				return
			}
			// 既没有采样应答、也没有任何流量，才真正判不通
			if consecutiveFail >= qualityFailThreshold {
				st.netOK = false
				st.netErr = info
				st.probeOK = false
				st.probeInfo = "隧道质量采样连续失败且无流量"
			}
		})
	}
}

func formatQualityInfo(rtt time.Duration) string {
	return "隧道往返 " + rtt.Round(time.Millisecond).String()
}