package api

import (
	"context"
	"encoding/json"
	"math"
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
	Country     string  `json:"country"`
	CountryCode string  `json:"countryCode"`
	Region      string  `json:"regionName"`
	City        string  `json:"city"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Status      string  `json:"status"`
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
	u := "http://ip-api.com/json/" + url.PathEscape(ip) + "?fields=status,country,countryCode,regionName,city,lat,lon&lang=zh-CN"
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
			`SELECT id, COALESCE(NULLIF(last_ip4,''), NULLIF(last_ip,''), NULLIF(host,'')) AS ip
			   FROM servers
			  WHERE (ip_lat IS NULL OR COALESCE(ip_country,'') NOT REGEXP '^[A-Za-z]{2}$')
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
			country := strings.ToUpper(strings.TrimSpace(g.CountryCode))
			if len(country) != 2 {
				country = firstNonEmpty(g.Country, "")
			}
			region := strings.TrimSpace(strings.Join([]string{g.Region, g.City}, " "))
			_, _ = h.db.ExecContext(ctx,
				`UPDATE servers SET ip_country = IF(? <> '', ?, ip_country), ip_region = ?, ip_lat = ?, ip_lon = ? WHERE id = ?`,
				country, country, region, g.Lat, g.Lon, mapInt(row, "id", 0))
		}
	}()
}

var geoAllLast time.Time

// geoRefreshAllThrottled 定时刷新全部服务器归属地（默认 10 分钟一次）。
func (h *handlers) geoRefreshAllThrottled(ctx context.Context) {
	if time.Since(geoAllLast) < 2*time.Minute {
		return
	}
	geoAllLast = time.Now()
	h.geoRefreshAll(ctx)
}

// geoRefreshAll 按当前公网 IP 重新查询并更新归属地；IP 变化时国旗/坐标随之更新。
func (h *handlers) geoRefreshAll(ctx context.Context) {
	rows, err := queryRowsAsMaps(ctx, h.db,
		`SELECT id, COALESCE(NULLIF(last_ip4,''), NULLIF(last_ip,''), NULLIF(host,'')) AS ip,
		        COALESCE(ip_country,'') AS ip_country, COALESCE(ip_region,'') AS ip_region,
		        ip_lat, ip_lon
		   FROM servers
		  WHERE COALESCE(NULLIF(last_ip4,''), NULLIF(last_ip,''), NULLIF(host,'')) <> ''`)
	if err != nil {
		return
	}
	changed := 0
	for _, row := range rows {
		if h.applyGeoRow(ctx, row) {
			changed++
		}
	}
	if changed > 0 {
		h.log.Info("server geo refreshed", "total", len(rows), "changed", changed)
	}
	_ = changed
}

// geoRefreshServer 只刷新一台服务器（新增服务器后调用）。
func (h *handlers) geoRefreshServer(ctx context.Context, id int64) {
	rows, err := queryRowsAsMaps(ctx, h.db,
		`SELECT id, COALESCE(NULLIF(last_ip4,''), NULLIF(last_ip,''), NULLIF(host,'')) AS ip,
		        COALESCE(ip_country,'') AS ip_country, COALESCE(ip_region,'') AS ip_region,
		        ip_lat, ip_lon
		   FROM servers WHERE id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		return
	}
	h.applyGeoRow(ctx, rows[0])
}

func (h *handlers) applyGeoRow(ctx context.Context, row map[string]any) bool {
	ip := mapStr(row, "ip", "")
	if ip == "" {
		return false
	}
	g, ok := h.geoLookupIP(ctx, ip)
	if !ok {
		return false
	}
	country := strings.ToUpper(strings.TrimSpace(g.CountryCode))
	if len(country) != 2 {
		country = firstNonEmpty(g.Country, "")
	}
	region := strings.TrimSpace(strings.Join([]string{g.Region, g.City}, " "))
	oldCountry := mapStr(row, "ip_country", "")
	oldRegion := mapStr(row, "ip_region", "")
	oldLat := jsonNum(row["ip_lat"])
	oldLon := jsonNum(row["ip_lon"])
	if oldCountry == country && oldRegion == region && math.Abs(oldLat-g.Lat) < 0.0001 && math.Abs(oldLon-g.Lon) < 0.0001 {
		return false
	}
	_, _ = h.db.ExecContext(ctx,
		`UPDATE servers SET ip_country = IF(? <> '', ?, ip_country), ip_region = ?, ip_lat = ?, ip_lon = ? WHERE id = ?`,
		country, country, region, g.Lat, g.Lon, mapInt(row, "id", 0))
	return true
}
