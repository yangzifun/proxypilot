package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// 服务器 IP 地理位置补全：用 ip-api.com 免费接口按服务器出口 IP 查询，
// 结果写回 servers.ip_country / ip_region / ip_lat / ip_lon，供地图定位。

type ipGeo struct {
	Country string  `json:"country"`
	Region  string  `json:"regionName"`
	City    string  `json:"city"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Status  string  `json:"status"`
}

var (
	geoCache   sync.Map // ip -> ipGeo
	geoFilling sync.Mutex
	geoLastRun time.Time
)

func (h *handlers) geoLookupIP(ctx context.Context, ip string) (ipGeo, bool) {
	ip = strings.TrimSpace(ip)
	if ip == "" || net.ParseIP(ip) == nil {
		return ipGeo{}, false
	}
	if v, ok := geoCache.Load(ip); ok {
		return v.(ipGeo), true
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	u := "http://ip-api.com/json/" + url.PathEscape(ip) + "?fields=status,country,regionName,city,lat,lon&lang=zh-CN"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ipGeo{}, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ipGeo{}, false
	}
	defer resp.Body.Close()
	var g ipGeo
	if json.NewDecoder(resp.Body).Decode(&g) != nil || !strings.EqualFold(g.Status, "success") {
		return ipGeo{}, false
	}
	if g.Lat == 0 && g.Lon == 0 {
		return ipGeo{}, false
	}
	geoCache.Store(ip, g)
	return g, true
}

// fillServerGeoAsync 在后台补全缺少经纬度的服务器（节流：最多每 20 秒跑一轮）。
func (h *handlers) fillServerGeoAsync(ctx context.Context) {
	if !geoFilling.TryLock() {
		return
	}
	go func() {
		defer geoFilling.Unlock()
		if time.Since(geoLastRun) < 20*time.Second {
			return
		}
		geoLastRun = time.Now()

		rows, err := queryRowsAsMaps(ctx, h.db,
			`SELECT id, COALESCE(NULLIF(last_ip4,''), NULLIF(last_ip,'')) AS ip
			   FROM servers
			  WHERE ip_lat IS NULL
			    AND COALESCE(NULLIF(last_ip4,''), NULLIF(last_ip,'')) <> ''
			  ORDER BY id ASC LIMIT 20`)
		if err != nil {
			return
		}
		for _, row := range rows {
			g, ok := h.geoLookupIP(ctx, mapStr(row, "ip", ""))
			if !ok {
				continue
			}
			country := firstNonEmpty(g.Country, "")
			region := strings.TrimSpace(strings.Join([]string{g.Region, g.City}, " "))
			_, _ = h.db.ExecContext(ctx,
				`UPDATE servers SET ip_country = ?, ip_region = ?, ip_lat = ?, ip_lon = ? WHERE id = ?`,
				country, region, g.Lat, g.Lon, mapInt(row, "id", 0))
		}
	}()
}
