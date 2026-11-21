package api

import "testing"

func TestParseComponentVersionsJSON(t *testing.T) {
	body := []byte(`{
	  "default": "1.14.4",
	  "versions": [
	    {"version": "1.14.4", "files": {"linux-amd64": {"binary":"sing-box-linux-amd64","tar":"sing-box-1.14.4-linux-amd64.tar.gz","sha256":"aaa","tar_sha256":"bbb"}}},
	    {"version": "1.15.0-alpha.13", "prerelease": true, "files": {"linux-amd64": {"binary":"sing-box-linux-amd64-1.15.0-alpha.13","tar":"sing-box-1.15.0-alpha.13-linux-amd64.tar.gz","sha256":"ccc","tar_sha256":"ddd"}}}
	  ]
	}`)
	list, err := parseComponentVersionsJSON(body, "versions.json")
	if err != nil || len(list) != 2 {
		t.Fatalf("list=%#v err=%v", list, err)
	}
	if !list[0].Default || list[0].Version != "1.14.4" {
		t.Fatalf("默认版本应排第一：%#v", list[0])
	}
	if list[1].Version != "1.15.0-alpha.13" || !list[1].Prerelease {
		t.Fatalf("预览版本解析错误：%#v", list[1])
	}
	if list[1].Files["linux-amd64"].Tar != "sing-box-1.15.0-alpha.13-linux-amd64.tar.gz" {
		t.Fatalf("tar 解析错误：%#v", list[1].Files)
	}
}

func TestParseComponentManifestFallback(t *testing.T) {
	body := []byte(`{"version":"1.14.4","released_at":"2026-11-12T07:13:39Z","files":{"linux-amd64":{"binary":"sing-box-linux-amd64","tar":"sing-box-1.14.4-linux-amd64.tar.gz","sha256":"aaa","tar_sha256":"bbb"}}}`)
	list, err := parseComponentVersionsJSON(body, "manifest.json")
	if err != nil || len(list) != 1 || list[0].Version != "1.14.4" {
		t.Fatalf("list=%#v err=%v", list, err)
	}
}

func TestParseComponentVersionsArray(t *testing.T) {
	body := []byte(`[{"tag":"v1.15.0-alpha.13","prerelease":true,"files":{"linux-amd64":{"tar":"x.tar.gz"}}}]`)
	list, err := parseComponentVersionsJSON(body, "versions.json")
	if err != nil || len(list) != 1 || list[0].Version != "1.15.0-alpha.13" || !list[0].Prerelease {
		t.Fatalf("list=%#v err=%v", list, err)
	}
}

func TestCompareComponentVersion(t *testing.T) {
	if compareVersion("1.15.0-alpha.13", "1.14.4") <= 0 {
		t.Fatal("1.15 应大于 1.14")
	}
	if compareVersion("1.14.4", "1.15.0-alpha.13") >= 0 {
		t.Fatal("1.14 应小于 1.15")
	}
	if compareVersion("1.14.10", "1.14.9") <= 0 {
		t.Fatal("数值段比较错误")
	}
}
