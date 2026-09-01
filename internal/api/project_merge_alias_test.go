package api

import (
	"strings"
	"testing"

	"github.com/hoveychen/muvee/internal/store"
)

// mergeProjectUpdate 里 `p := *existing` 是**浅拷贝**：指针字段与 existing 共享
// 同一个被指对象。而 encoding/json 对**非 nil 指针字段**是「写穿」的——它不会
// 新分配，而是把新值写进原来那个对象。结果 existing 被一起改掉，随后所有
// `xxxChanged(existing, p)` 的比较都退化成自己跟自己比，永远返回「没变」。
//
// 后果不是脏数据，而是**闸门整个失灵**：
//   - tcpRouteChanged → TCP 域名撞名检查与 admin 闸都不跑（2026-09-01 在线上
//     实测到：把媒体项目的 tcp 域名改成另一个项目的 domain_prefix，返回 200
//     并且真的存进去了；而同样的值走 create 路径会被正确地 409 拒掉）。
//   - fixedPortChanged → 同一个毛病，且它更早就在：一个**已经**设过固定端口的
//     项目，非 admin 也能改它的 fixed_host_port，端口占用检查也一并跳过。
func TestMergeProjectUpdateDoesNotMutateExisting(t *testing.T) {
	prefix, port := "lkturn", 15349
	existing := &store.Project{
		Name: "media", DomainPrefix: "lk",
		TCPDomainPrefix: &prefix, TCPHostPort: &port,
	}
	body := `{"tcp_domain_prefix":"expertcall","tcp_host_port":16000}`

	p, err := mergeProjectUpdate(existing, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if *p.TCPDomainPrefix != "expertcall" || *p.TCPHostPort != 16000 {
		t.Fatalf("新值没合进来: %v %v", *p.TCPDomainPrefix, *p.TCPHostPort)
	}
	// 关键：existing 必须还是原样，否则下面的变更检测就瞎了
	if *existing.TCPDomainPrefix != "lkturn" {
		t.Errorf("existing.TCPDomainPrefix 被写穿成了 %q", *existing.TCPDomainPrefix)
	}
	if *existing.TCPHostPort != 15349 {
		t.Errorf("existing.TCPHostPort 被写穿成了 %d", *existing.TCPHostPort)
	}
	if !tcpRouteChanged(existing, p) {
		t.Error("变更检测失灵 —— 撞名检查与 admin 闸都会被跳过")
	}
}

// 同一个根因在 fixed port 上：这条钉住那个更早就存在的洞。
func TestMergeProjectUpdateDetectsFixedPortChange(t *testing.T) {
	port := 30001
	existing := &store.Project{Name: "x", DomainPrefix: "x", FixedHostPort: &port}
	p, err := mergeProjectUpdate(existing, strings.NewReader(`{"fixed_host_port":30002}`))
	if err != nil {
		t.Fatal(err)
	}
	if *existing.FixedHostPort != 30001 {
		t.Errorf("existing.FixedHostPort 被写穿成了 %d", *existing.FixedHostPort)
	}
	if !fixedPortChanged(existing, p) {
		t.Error("固定端口的变更检测失灵 —— admin 闸与端口占用检查会被跳过")
	}
}
