package tests

import (
	"errors"
	"net"
	"testing"

	"llm-hub/src/hub"
)

// publicResolver 返回公网 IP，避免测试依赖真实 DNS。
func publicResolver(_ string) ([]net.IP, error) {
	return []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("2001:4860:4860::8888")}, nil
}

func TestSSRFRejectPrivateAndMetadata(t *testing.T) {
	bad := []string{
		"http://127.0.0.1:8080",
		"http://10.0.0.1",
		"http://172.16.0.1",
		"http://172.31.255.255",
		"http://192.168.1.1",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
		"http://0.0.0.0",
		"http://100.64.0.1",
		"file:///etc/passwd",
		"gopher://127.0.0.1:6379/_INFO",
		"ftp://internal.example.com",
		"http://localhost",
		"http://foo.localhost",
		"http://user:pass@example.com",
	}
	for _, uri := range bad {
		if err := hub.ValidateBaseURI(uri); err == nil {
			t.Errorf("应拒绝 %q", uri)
		}
	}
}

func TestSSRFRejectInternalLiteralWithoutDNS(t *testing.T) {
	// 字面 IP 直接判定，不触发 DNS；确保 ValidateBaseURI 离线可用
	resolverCalled := false
	lookup := func(host string) ([]net.IP, error) {
		resolverCalled = true
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	}
	if err := hub.ValidateBaseURIWithResolver("http://127.0.0.1:9000", lookup); err == nil {
		t.Error("字面回环 IP 应被拒绝")
	}
	if resolverCalled {
		t.Error("字面 IP 判定不应调用 DNS 解析")
	}
}

func TestSSRFAllowPublicHost(t *testing.T) {
	if err := hub.ValidateBaseURIWithResolver("https://api.example.com/v1", publicResolver); err != nil {
		t.Fatalf("公网域名应通过: %v", err)
	}
	if err := hub.ValidateBaseURIWithResolver("https://api.example.com:8443/v1", publicResolver); err != nil {
		t.Fatalf("公网域名带端口应通过: %v", err)
	}
}

func TestSSRFDNSRebindingSecondCheck(t *testing.T) {
	// 主机名解析到内网地址时二次校验必须拒绝
	lookup := func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.1.2.3")}, nil
	}
	if err := hub.ValidateBaseURIWithResolver("https://evil.example.com/v1", lookup); err == nil {
		t.Fatal("解析到 10/8 的地址应被拒绝")
	}
	lookup2 := func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("127.0.0.1")}, nil
	}
	if err := hub.ValidateBaseURIWithResolver("https://mixed.example.com/v1", lookup2); err == nil {
		t.Fatal("任一解析结果命中内网即应拒绝")
	}
}

func TestSSRFRejectResolutionFailure(t *testing.T) {
	lookup := func(host string) ([]net.IP, error) {
		return nil, errors.New("no such host")
	}
	if err := hub.ValidateBaseURIWithResolver("https://unknown.example.com/v1", lookup); err == nil {
		t.Fatal("无法解析的主机名应拒绝（无法二次校验）")
	}
	if err := hub.ValidateBaseURIWithResolver("", publicResolver); err == nil {
		t.Fatal("空 base_uri 应拒绝")
	}
}
