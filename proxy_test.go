package main

import (
	"context"
	"io/ioutil"
	"net/http"
	"net/url"
	"testing"
)

func TestProxy(t *testing.T) {
	c := http.Client{
		Transport: &http.Transport{
			Proxy: func(r *http.Request) (*url.URL, error) {
				return url.Parse("http://127.0.0.1:8080")
			},
			GetProxyConnectHeader: func(ctx context.Context, proxyURL *url.URL, target string) (http.Header, error) {
				h := http.Header{}
				if target == "www.baidu.com:443" {
					h.Set("User-Agent", "[RULE]www.baidu.com:443@www.baidu.com:443$curl/7.79.1")
					h.Set("Test-Header", "sdfd")
				}
				return h, nil
			},
		},
	}

	req, _ := http.NewRequest(http.MethodGet, "https://www.baidu.com", nil)
	rsp, err := c.Do(req)
	if err != nil {
		panic(err)
	}
	rsp.Body.Close()

	data, _ := ioutil.ReadAll(rsp.Body)
	t.Logf("%s\n", data)
}
