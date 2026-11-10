package main

import (
	"net"
	"net/http"
	"time"
)

// panelHTTP 是所有「客户端 ↔ 面板」控制面请求专用的客户端。
//
// 关键点：Proxy 显式设为 nil。
//
// 墨菲定律在这里一定会发生：用户机器上往往装着 Clash / v2ray 之类的本地代理
// （127.0.0.1:7890），而 http.DefaultClient 会走 ProxyFromEnvironment，
// 于是客户端自己的「拉节点、换票据、上报心跳」全被塞进那个代理；
// 一旦代理没开、或者代理本身依赖 VPN 才能出网，就形成死循环 ——
// 表现为「获取不到节点信息」「连接失败」，而服务端日志干干净净。
//
// VPN 客户端的管理流量必须直连，绝不能经过任何代理。
var panelHTTP = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		Proxy: nil, // 直连，永不使用系统/环境代理
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	},
}
// loopbackHTTP 用于访问本机 UI 服务（127.0.0.1:xxxx）。
// 同样必须绕开代理：否则本地代理一挂，连自己的界面接口都调不通。
var loopbackHTTP = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   3 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	},
}
