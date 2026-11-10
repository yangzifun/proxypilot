//go:build windows

package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wintun"
)

// 直接用 Wintun 的 ring API，绕过 golang.zx2c4.com/wireguard/tun 封装。
//
// 为什么换：封装层的 Read 是
//	packet, _ := session.ReceivePacket()
//	n := copy(bufs[0][offset:], packet)
//	sizes[0] = n
// 复用同一组 bufs/sizes，并把 ring 里的包再拷一次。实测出现「同一个报文
// 两次交付长度不同（40 / 52），内容一致」，怀疑就在这层。直接用 ring
// API 可以拿到驱动原始报的 packetSize，并且少一次拷贝。

// defaultTunName 各平台默认网卡名。
func defaultTunName() string {
	return "ProxyPilot VPN"
}

const (
	// ring 容量：Wintun 要求 2 的幂，且 >= 0x20000。这里取 4MB。
	wintunRingCapacity = 0x400000
	wintunTunnelType   = "ProxyPilot"
)

type wintunDevice struct {
	adapter *wintun.Adapter
	session wintun.Session
	name    string
	event   windows.Handle

	mu     sync.Mutex
	closed bool
}

func (d *wintunDevice) Name() string { return d.name }

// Read 直接从 ring 取一个包拷进 p。
//
// ring 里没有包时等待驱动的事件对象，避免忙轮询。
func (d *wintunDevice) Read(p []byte) (int, error) {
	for {
		d.mu.Lock()
		closed := d.closed
		d.mu.Unlock()
		if closed {
			return 0, fmt.Errorf("网卡已关闭")
		}

		pkt, err := d.session.ReceivePacket()
		if err == nil {
			// pkt 是 ring 内的切片，长度由驱动给出；必须在 Release 之前拷走
			n := copy(p, pkt)
			d.session.ReleaseReceivePacket(pkt)
			return n, nil
		}
		if err != windows.ERROR_NO_MORE_ITEMS {
			return 0, fmt.Errorf("读取网卡失败：%w", err)
		}
		// 环空了：等驱动通知
		if _, werr := windows.WaitForSingleObject(d.event, windows.INFINITE); werr != nil {
			return 0, fmt.Errorf("等待网卡数据失败：%w", werr)
		}
	}
}

// Write 用 AllocateSendPacket 直接写进 ring。
func (d *wintunDevice) Write(p []byte) (int, error) {
	d.mu.Lock()
	closed := d.closed
	d.mu.Unlock()
	if closed {
		return 0, fmt.Errorf("网卡已关闭")
	}
	buf, err := d.session.AllocateSendPacket(len(p))
	if err != nil {
		return 0, fmt.Errorf("申请发送缓冲失败：%w", err)
	}
	copy(buf, p)
	d.session.SendPacket(buf)
	return len(p), nil
}

func (d *wintunDevice) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	d.mu.Unlock()

	// 先唤醒可能卡在 Read 的协程
	safeSetEvent(d.event)
	d.session.End()
	return d.adapter.Close()
}

func safeSetEvent(h windows.Handle) {
	if h == 0 {
		return
	}
	defer func() { _ = recover() }() // 句柄可能已被 session.End 释放
	_ = windows.SetEvent(h)
}

// openTUN 在 Windows 上用 Wintun 创建虚拟网卡（需要管理员权限）。
func openTUN(name string) (PacketDevice, error) {
	if err := ensureWintun(); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultTunName()
	}

	adapter, err := wintun.CreateAdapter(name, wintunTunnelType, nil)
	if err != nil {
		// 已存在同名网卡时，直接打开它
		if adapter2, oerr := wintun.OpenAdapter(name); oerr == nil {
			adapter = adapter2
		} else {
			return nil, fmt.Errorf("创建 Wintun 网卡失败（需要管理员权限，且 wintun.dll 必须存在）：%w", err)
		}
	}

	realName := name
	if luid := adapter.LUID(); luid != 0 {
		if n, nerr := adapterNameByLUID(luid); nerr == nil && strings.TrimSpace(n) != "" {
			realName = n
		}
	}

	session, err := adapter.StartSession(wintunRingCapacity)
	if err != nil {
		_ = adapter.Close()
		return nil, fmt.Errorf("启动 Wintun 会话失败：%w", err)
	}

	d := &wintunDevice{
		adapter: adapter,
		session: session,
		name:    realName,
		event:   session.ReadWaitEvent(),
	}

	// 关掉网卡的发送卸载：开了 LSO 之后 Windows 会把「大段」交给网卡去切分，
	// 交上来的报文 IP 头声明长度可能远大于实际字节数。
	disableTunOffload(realName)
	log.Printf("数据面：已启用 Wintun 直连 ring API（%s，ring=%d 字节）", realName, wintunRingCapacity)

	return d, nil
}

// adapterNameByLUID 通过 LUID 查回网卡别名。
// CreateAdapter 传的名字在重名时可能被系统加后缀，路由/防火墙都按别名操作，
// 所以必须拿真实别名。
func adapterNameByLUID(luid uint64) (string, error) {
	var size uint32
	err := windows.GetAdaptersAddresses(windows.AF_UNSPEC,
		windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER,
		0, nil, &size)
	if err != nil && err != windows.ERROR_BUFFER_OVERFLOW {
		return "", err
	}
	if size == 0 {
		return "", fmt.Errorf("网卡列表为空")
	}
	buf := make([]byte, size)
	aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
	if err := windows.GetAdaptersAddresses(windows.AF_UNSPEC,
		windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER,
		0, aa, &size); err != nil {
		return "", err
	}
	for cur := aa; cur != nil; cur = cur.Next {
		if cur.Luid == luid {
			name := windows.UTF16PtrToString(cur.FriendlyName)
			if strings.TrimSpace(name) != "" {
				return name, nil
			}
		}
	}
	return "", fmt.Errorf("未找到 LUID %d 对应的网卡", luid)
}
