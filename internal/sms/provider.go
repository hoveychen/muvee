package sms

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	dypnsapi "github.com/alibabacloud-go/dypnsapi-20170525/v2/client"
	"github.com/alibabacloud-go/tea/tea"
)

// VerifyProvider sends and verifies phone login codes. With Aliyun PNVS
// (号码认证服务 短信认证) the platform generates and verifies the code, so the
// server never sees it; the dev fallback does it locally so the flow can be
// exercised without Aliyun.
type VerifyProvider interface {
	SendCode(ctx context.Context, phone string) error
	CheckCode(ctx context.Context, phone, code string) (bool, error)
}

// NewVerifyProviderFromEnv returns a PNVSProvider when all four ALIYUN_SMS_*
// variables are set, otherwise a LogVerifyProvider. SignName/TemplateCode come
// from the PNVS console (号码认证服务 → 短信认证, no enterprise qualification
// required for individuals). Fill the env vars and restart to go live — no code
// change needed.
func NewVerifyProviderFromEnv() VerifyProvider {
	id := os.Getenv("ALIYUN_SMS_ACCESS_KEY_ID")
	secret := os.Getenv("ALIYUN_SMS_ACCESS_KEY_SECRET")
	sign := os.Getenv("ALIYUN_SMS_SIGN_NAME")
	tmpl := os.Getenv("ALIYUN_SMS_TEMPLATE_CODE")
	if id == "" || secret == "" || sign == "" || tmpl == "" {
		log.Printf("[sms] ALIYUN_SMS_* not fully configured; using dev LogVerifyProvider")
		return NewLogVerifyProvider()
	}
	client, err := dypnsapi.NewClient(&openapi.Config{
		AccessKeyId:     tea.String(id),
		AccessKeySecret: tea.String(secret),
		Endpoint:        tea.String("dypnsapi.aliyuncs.com"),
	})
	if err != nil {
		log.Printf("[sms] PNVS client init failed (%v); using dev LogVerifyProvider", err)
		return NewLogVerifyProvider()
	}
	// The verification-code template variable defaults to ##code## (PNVS auto
	// fills the generated code). Overridable via env in case the console
	// template uses a different variable name.
	tp := os.Getenv("ALIYUN_SMS_TEMPLATE_PARAM")
	if tp == "" {
		tp = `{"code":"##code##"}`
	}
	return &PNVSProvider{client: client, signName: sign, templateCode: tmpl, templateParam: tp}
}

// PNVSProvider calls Aliyun 号码认证服务 (Dypnsapi) SendSmsVerifyCode /
// CheckSmsVerifyCode. The platform owns code generation, lifecycle and
// verification.
type PNVSProvider struct {
	client        *dypnsapi.Client
	signName      string
	templateCode  string
	templateParam string
}

func (p *PNVSProvider) SendCode(_ context.Context, phone string) error {
	resp, err := p.client.SendSmsVerifyCode(&dypnsapi.SendSmsVerifyCodeRequest{
		PhoneNumber:   tea.String(domesticPhone(phone)),
		SignName:      tea.String(p.signName),
		TemplateCode:  tea.String(p.templateCode),
		TemplateParam: tea.String(p.templateParam),
		CodeLength:    tea.Int64(6),
		ValidTime:     tea.Int64(300), // 5 minutes, matches the message copy
	})
	if err != nil {
		return err
	}
	if resp == nil || resp.Body == nil || tea.StringValue(resp.Body.Code) != "OK" {
		return fmt.Errorf("aliyun pnvs send failed: %s", respMessage(resp))
	}
	return nil
}

func (p *PNVSProvider) CheckCode(_ context.Context, phone, code string) (bool, error) {
	resp, err := p.client.CheckSmsVerifyCode(&dypnsapi.CheckSmsVerifyCodeRequest{
		PhoneNumber: tea.String(domesticPhone(phone)),
		VerifyCode:  tea.String(code),
	})
	if err != nil {
		return false, err
	}
	if resp == nil || resp.Body == nil || tea.StringValue(resp.Body.Code) != "OK" {
		return false, fmt.Errorf("aliyun pnvs check failed: %s", checkRespMessage(resp))
	}
	if resp.Body.Model == nil {
		return false, nil
	}
	return tea.StringValue(resp.Body.Model.VerifyResult) == "PASS", nil
}

func respMessage(r *dypnsapi.SendSmsVerifyCodeResponse) string {
	if r == nil || r.Body == nil {
		return "empty response"
	}
	return tea.StringValue(r.Body.Code) + ": " + tea.StringValue(r.Body.Message)
}

func checkRespMessage(r *dypnsapi.CheckSmsVerifyCodeResponse) string {
	if r == nil || r.Body == nil {
		return "empty response"
	}
	return tea.StringValue(r.Body.Code) + ": " + tea.StringValue(r.Body.Message)
}

// LogVerifyProvider is the dev fallback used when PNVS is not configured. It
// generates the code locally, logs it, and verifies it in-process so the login
// flow works end-to-end without Aliyun. Single-process only (not for prod).
type LogVerifyProvider struct {
	mu    sync.Mutex
	codes map[string]devCode
}

type devCode struct {
	code    string
	expires time.Time
}

func NewLogVerifyProvider() *LogVerifyProvider {
	return &LogVerifyProvider{codes: make(map[string]devCode)}
}

func (l *LogVerifyProvider) SendCode(_ context.Context, phone string) error {
	code, err := generateNumericCode()
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.codes[phone] = devCode{code: code, expires: time.Now().Add(5 * time.Minute)}
	l.mu.Unlock()
	log.Printf("[sms][DEV MODE] verification code for %s = %s "+
		"(set ALIYUN_SMS_* to send real SMS via PNVS)", phone, code)
	return nil
}

func (l *LogVerifyProvider) CheckCode(_ context.Context, phone, code string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.codes[phone]
	if !ok || time.Now().After(c.expires) || c.code != code {
		return false, nil
	}
	delete(l.codes, phone) // single-use
	return true, nil
}

// generateNumericCode returns a random 6-digit code.
func generateNumericCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// domesticPhone converts an E.164 number to the bare form PNVS expects (PNVS
// SMS-auth serves Chinese-mainland numbers; country code defaults to 86).
func domesticPhone(e164 string) string {
	if strings.HasPrefix(e164, "+86") {
		return e164[3:]
	}
	return strings.TrimPrefix(e164, "+")
}
