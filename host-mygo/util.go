package main

import (
	"fmt"
	"net/url"
	"os"
)

// parseProxy 解析代理地址，缺省补 http:// 前缀。
func parseProxy(s string) (*url.URL, error) {
	if s == "" {
		return nil, fmt.Errorf("empty")
	}
	if u, err := url.Parse(s); err == nil && u.Scheme != "" {
		return u, nil
	}
	return url.Parse("http://" + s)
}

// logErr 统一打印某个 provider 的取数错误（不含任何 token）。
func logErr(provider string, err error) {
	fmt.Fprintf(os.Stderr, "[error][%s] %v\n", provider, err)
}

// truncate 截断字节串用于调试打印。
func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…(truncated)"
	}
	return string(b)
}
