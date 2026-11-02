package metrics

import "testing"

const procNetDevFixture = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 1000 10 0 0 0 0 0 0 1000 10 0 0 0 0 0 0
  eth0: 2048 20 1 0 0 0 0 0 4096 40 2 0 0 0 0 0
veth1a2b3c: 999 9 0 0 0 0 0 0 999 9 0 0 0 0 0 0
docker0: 512 4 0 0 0 0 0 0 512 4 0 0 0 0 0 0
   wg0: 100 1 0 0 0 0 0 0 200 2 0 0 0 0 0 0
`

func TestParseProcNetDev(t *testing.T) {
	got := ParseProcNetDev(procNetDevFixture)
	want := NetStats{RxBytes: 2148, TxBytes: 4296, RxPackets: 21, TxPackets: 42, RxErrs: 1, TxErrs: 2, Ifaces: 2}
	if got != want {
		t.Fatalf("ParseProcNetDev=%+v，期望 %+v（lo/veth/docker 应被排除，wg0 应计入）", got, want)
	}
	if empty := ParseProcNetDev(""); empty.Ifaces != 0 || empty.RxBytes != 0 {
		t.Fatalf("空输入应为零值：%+v", empty)
	}
}

const diskStatsFixture = `   8       0 sda 1000 0 2000 0 200 0 4000 0 0 0 0 0 0 0
   8       1 sda1 900 0 1800 0 190 0 3800 0 0 0 0 0 0 0
 259       0 nvme0n1 500 0 1000 0 100 0 2000 0 0 0 0 0 0 0
 259       1 nvme0n1p1 400 0 800 0 90 0 1600 0 0 0 0 0 0 0
   7       0 loop0 10 0 20 0 0 0 0 0 0 0 0 0 0 0
 253       0 dm-0 50 0 100 0 10 0 20 0 0 0 0 0 0 0
   9       0 md0 30 0 60 0 3 0 6 0 0 0 0 0 0 0
 254       0 vda 2000 0 4000 0 300 0 6000 0 0 0 0 0 0 0
`

func TestParseDiskStats(t *testing.T) {
	got := ParseDiskStats(diskStatsFixture)
	want := DiskIO{ReadBytes: 3584000, WriteBytes: 6144000, Reads: 3500, Writes: 600, Devices: 3}
	if got != want {
		t.Fatalf("ParseDiskStats=%+v，期望 %+v（分区/loop/dm-/md 应被排除）", got, want)
	}
	if isPartitionName("nvme0n1") || !isPartitionName("nvme0n1p1") || !isPartitionName("sda1") || !isPartitionName("xvdab12") {
		t.Fatalf("分区名判定有误：nvme0n1=%v nvme0n1p1=%v sda1=%v xvdab12=%v",
			isPartitionName("nvme0n1"), isPartitionName("nvme0n1p1"), isPartitionName("sda1"), isPartitionName("xvdab12"))
	}
}

const procNetStatesFixture = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345
   1: 00000000:1F90 00000000:0000 01 00000000:00000000 00:00000000 00000000     0        0 12346
   2: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12347
   3: 0100007F:1F90 0100007F:0016 06 00000000:00000000 00:00000000 00000000     0        0 12348
   4: 0100007F:1F91 0100007F:0016 08 00000000:00000000 00:00000000 00000000     0        0 12349
   5: 0100007F:1F92 0100007F:0016 02 00000000:00000000 00:00000000 00000000     0        0 12350
   6: 0100007F:1F93 0100007F:0016 07 00000000:00000000 00:00000000 00000000     0        0 12351
`

func TestParseProcNetStates(t *testing.T) {
	got := ParseProcNetStates(procNetStatesFixture)
	want := ConnStates{Total: 7, Established: 1, Listen: 2, TimeWait: 1, CloseWait: 1, SynSent: 1, Other: 1}
	if got != want {
		t.Fatalf("ParseProcNetStates=%+v，期望 %+v", got, want)
	}
	sum := sumConn(got, ConnStates{Total: 1, Established: 1})
	if sum.Total != 8 || sum.Established != 2 {
		t.Fatalf("sumConn=%+v", sum)
	}
}

func TestParseProcStat(t *testing.T) {
	// 字段 3 起：state + ppid..cmajflt(10) + utime(14) + stime(15) + cutime..vsize(8) + rss(24)
	text := "1234 (nginx worker) S 1 1 1 0 -1 4194560 100 0 0 0 777 123 0 0 20 0 3 0 123456 789 4096\n"
	name, ticks, rssPages, ok := ParseProcStat(text)
	if !ok || name != "nginx worker" || ticks != 900 || rssPages != 4096 {
		t.Fatalf("ParseProcStat=%q/%d/%d/%v，期望 nginx worker/900/4096/true", name, ticks, rssPages, ok)
	}
	if _, _, _, ok := ParseProcStat("bad"); ok {
		t.Fatalf("非法输入应返回 ok=false")
	}
	if got := ParseProcStatusRSS("Name:\tnginx\nVmRSS:\t  2048 kB\n"); got != 2 {
		t.Fatalf("ParseProcStatusRSS=%d，期望 2", got)
	}
}

func TestListProcStatsSafe(t *testing.T) {
	// Windows / 无 /proc 环境：应安全返回 0，不 panic
	info := ListProcStats()
	if info.Total < 0 || info.Top == nil {
		t.Fatalf("ListProcStats=%+v", info)
	}
}
