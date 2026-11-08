// Package ssrf 对贡献渠道的 base_uri 做白名单校验 + DNS 解析二次校验，
// 拒绝内网/回环/链路本地/元数据地址与危险协议，防止服务端请求伪造。
package ssrf

import (
	"os"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Resolver 是可注入的 DNS 解析函数，便于离线单元测试。
type Resolver func(host string) ([]net.IP, error)

// defaultResolver 走系统 DNS。
func defaultResolver(host string) ([]net.IP, error) { return net.LookupIP(host) }

// AllowPrivateEnv 是唯一的放行开关：仅当环境变量
// ALLOW_PRIVATE_UPSTREAM=1（或 true）时，才允许把回环/内网地址作为上游 base_uri。
//
// 为什么需要它：本机开发与本机靶场（deploy/target-range.sh、mock 上游）必须能指向
// 127.0.0.1，否则无法做端到端联调。默认关闭 —— 生产部署不设此变量时，SSRF 防护
// 与原来完全一致，回环/内网/元数据地址一律拒绝。
//
// 该开关只影响出站上游校验，不影响任何入站鉴权或防火墙判定。
func AllowPrivateEnv() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("ALLOW_PRIVATE_UPSTREAM")))
	return v == "1" || v == "true" || v == "yes"
}

// Validate 使用系统 DNS 校验 base_uri。
func Validate(baseURI string) error {
	if AllowPrivateEnv() {
		return ValidateSchemeOnly(baseURI)
	}
	return ValidateWithResolver(baseURI, defaultResolver)
}

// ValidateSchemeOnly 只做协议白名单校验（供 ALLOW_PRIVATE_UPSTREAM=1 的开发/靶场模式）。
// 仍然拒绝 file/gopher/ftp 等危险协议与非 http(s) 输入。
func ValidateSchemeOnly(baseURI string) error {
	raw := strings.TrimSpace(baseURI)
	if raw == "" {
		return fmt.Errorf("base_uri 为空")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("base_uri 解析失败: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("拒绝协议 %q：仅允许 http/https（file/gopher/ftp 等一律拒绝）", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("base_uri 缺少主机名")
	}
	return nil
}

// ValidateWithResolver 使用指定解析器校验 base_uri（scheme 白名单 + 逐 IP 二次校验）。
func ValidateWithResolver(baseURI string, lookup Resolver) error {
	if lookup == nil {
		lookup = defaultResolver
	}
	raw := strings.TrimSpace(baseURI)
	if raw == "" {
		return fmt.Errorf("base_uri 为空")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("base_uri 解析失败: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("拒绝协议 %q：仅允许 http/https（file/gopher/ftp 等一律拒绝）", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("base_uri 缺少 host")
	}
	if u.User != nil {
		return fmt.Errorf("base_uri 不允许内嵌用户名/密码")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("base_uri host 为空")
	}
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") {
		return fmt.Errorf("拒绝本地主机名 %q", host)
	}

	// 字面 IP：直接判定，无需 DNS。
	if ip := net.ParseIP(h); ip != nil {
		if blocked, reason := isBlocked(ip); blocked {
			return fmt.Errorf("拒绝内网/回环/元数据地址 %s（%s）", ip.String(), reason)
		}
		return nil
	}

	// 拒绝非规范数字主机名（十进制/八进制/十六进制 IPv4 字面量，部分解析器
	// 会将其解析为回环/内网地址，形成黑名单绕过）。
	if looksNumericHost(h) {
		return fmt.Errorf("拒绝非规范数字主机名 %q", host)
	}

	// 主机名：解析后对每一个 A/AAAA 结果做二次校验，任一命中即拒绝。
	ips, err := lookup(h)
	if err != nil {
		return fmt.Errorf("无法解析 host %q 做二次校验: %w", h, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("host %q 解析结果为空", h)
	}
	for _, ip := range ips {
		if blocked, reason := isBlocked(ip); blocked {
			return fmt.Errorf("host %q 解析到被禁止地址 %s（%s）", h, ip.String(), reason)
		}
	}
	return nil
}

// isBlocked 判断一个 IP 是否属于被禁止访问的范围。
func isBlocked(ip net.IP) (bool, string) {
	if ip == nil {
		return true, "nil"
	}
	// IPv4-mapped IPv6 先转 IPv4。
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsUnspecified() {
		return true, "unspecified 0.0.0.0/8"
	}
	if ip.IsLoopback() {
		return true, "loopback 127/8 或 ::1"
	}
	if ip.IsPrivate() {
		return true, "RFC1918 10/8,172.16/12,192.168/16 或 ULA fc00::/7"
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true, "link-local 169.254/16 含 169.254.169.254 与 fe80::/10"
	}
	if ip.IsMulticast() {
		return true, "multicast"
	}
	// CGNAT 100.64.0.0/10 与云元数据常见附加段。
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return true, "CGNAT 100.64/10"
		}
		if v4[0] == 169 && v4[1] == 254 {
			return true, "link-local 169.254.169.254 元数据"
		}
		if v4[0] == 0 {
			return true, "0.0.0.0/8"
		}
		if v4[0] == 192 && v4[1] == 0 && v4[2] == 0 {
			return true, "192.0.0.0/24 IETF"
		}
	}
	return false, ""
}

// looksNumericHost 判定一个仅由数字/点/十六进制字符组成的主机名（非合法点分 IP），
// 这类名字在部分 libc 解析器里会被当作数值 IPv4 解释。
func looksNumericHost(h string) bool {
	if net.ParseIP(h) != nil {
		return false
	}
	if h == "" {
		return false
	}
	for _, r := range h {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') ||
			r == '.' || r == 'x' || r == 'X' {
			continue
		}
		return false
	}
	return true
}

// SafeClient 构造出站 HTTP 客户端：拨号时解析并二次校验每个 IP（防 DNS rebinding），
// 禁止跟随到内网/元数据地址的重定向，且限制重定向次数。
func SafeClient(timeout time.Duration, lookup Resolver) *http.Client {
	if lookup == nil {
		lookup = defaultResolver
	}
	// 开发/靶场放行模式：仅做协议与解析，不做内网判定（见 AllowPrivateEnv 注释）。
	// 生产部署不设 ALLOW_PRIVATE_UPSTREAM 时，下面所有内网拦截照常生效。
	allowPrivate := AllowPrivateEnv()
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := lookup(host)
			if err != nil || len(ips) == 0 {
				return nil, fmt.Errorf("ssrf: 拨号前解析 %q 失败", host)
			}
			if !allowPrivate {
				for _, ip := range ips {
					if blocked, reason := isBlocked(ip); blocked {
						return nil, fmt.Errorf("ssrf: 拨号目标 %s 被拒绝（%s）", ip.String(), reason)
					}
				}
			}
			// 固定到已校验 IP 拨号，消除校验与连接之间的 TOCTOU。
			d := net.Dialer{Timeout: 10 * time.Second}
			var lastErr error
			for _, ip := range ips {
				c, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return c, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("ssrf: 重定向次数过多")
			}
			if allowPrivate {
				return ValidateSchemeOnly(req.URL.String())
			}
			return ValidateWithResolver(req.URL.String(), lookup)
		},
	}
}
