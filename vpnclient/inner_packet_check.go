package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
)

// 内层报文自检：把「客户端读到的包」和「IP 头声明的长度」对一遍。
//
// 事故背景：节点日志里约 25% 的上行帧被判为坏包，hex 显示
//
//	frame_len=40  IP total=52     （TCP SYN，TCP 选项被截掉 12 字节）
//	frame_len=52  IP total=61     （LLMNR，少 9 字节）
//	frame_len=52  IP total=257    （TCP，少 205 字节）
//
// 也就是「发出去的字节数少于 IP 头声明的长度」。这个自检用来判定
// 截断到底发生在客户端读网卡那一刻，还是发生在节点解析那一刻。
func ipTotalLen(pkt []byte) int {
	if len(pkt) < 20 {
		return -1
	}
	switch pkt[0] >> 4 {
	case 4:
		return int(binary.BigEndian.Uint16(pkt[2:4]))
	case 6:
		if len(pkt) < 40 {
			return -1
		}
		return 40 + int(binary.BigEndian.Uint16(pkt[4:6]))
	default:
		return -1
	}
}

// checkInnerPacket 返回空字符串表示长度一致，否则返回问题描述。
func checkInnerPacket(pkt []byte) string {
	total := ipTotalLen(pkt)
	if total < 0 {
		return "无法识别 IP 版本"
	}
	if total == len(pkt) {
		return ""
	}
	return fmt.Sprintf("长度不一致：帧 %d 字节，IP 头声明 %d 字节（差 %d）",
		len(pkt), total, total-len(pkt))
}

// logInnerMismatch 只在前若干次打印，避免刷屏。
func logInnerMismatch(pkt []byte, seen int) {
	if seen > 8 {
		return
	}
	n := len(pkt)
	if n > 32 {
		n = 32
	}
	log.Printf("上行报文异常（第 %d 次）：%s | hex=% x", seen, checkInnerPacket(pkt), pkt[:n])
}

// describeTunRead 用于日志：把网卡读到的原始字节描述清楚。
func describeTunRead(raw []byte) string {
	n := len(raw)
	if n > 32 {
		n = 32
	}
	ver := -1
	if len(raw) > 0 {
		ver = int(raw[0] >> 4)
	}
	return fmt.Sprintf("网卡读数=%d ver=%d hex=% x", len(raw), ver, raw[:n])
}

var _ = net.IPv4len