package sms

import (
	"context"
	"regexp"
	"testing"
)

func TestLogVerifyProvider_SendThenCheck(t *testing.T) {
	p := NewLogVerifyProvider()
	ctx := context.Background()
	const phone = "+8613800138000"

	// Before any send, verification fails.
	if ok, _ := p.CheckCode(ctx, phone, "000000"); ok {
		t.Fatal("check should fail before a code is sent")
	}
	if err := p.SendCode(ctx, phone); err != nil {
		t.Fatalf("send: %v", err)
	}
	// Grab the code the dev provider stored.
	p.mu.Lock()
	code := p.codes[phone].code
	p.mu.Unlock()
	if !regexp.MustCompile(`^\d{6}$`).MatchString(code) {
		t.Fatalf("dev code %q is not 6 digits", code)
	}

	if ok, _ := p.CheckCode(ctx, phone, "999999"); ok && code != "999999" {
		t.Fatal("wrong code should not verify")
	}
	if ok, err := p.CheckCode(ctx, phone, code); err != nil || !ok {
		t.Fatalf("correct code should verify: ok=%v err=%v", ok, err)
	}
	// Single-use: the code is consumed on success.
	if ok, _ := p.CheckCode(ctx, phone, code); ok {
		t.Fatal("code should be single-use")
	}
}

func TestNewVerifyProviderFromEnv_FallsBackToLog(t *testing.T) {
	t.Setenv("ALIYUN_SMS_ACCESS_KEY_ID", "")
	t.Setenv("ALIYUN_SMS_ACCESS_KEY_SECRET", "")
	t.Setenv("ALIYUN_SMS_SIGN_NAME", "")
	t.Setenv("ALIYUN_SMS_TEMPLATE_CODE", "")
	if _, ok := NewVerifyProviderFromEnv().(*LogVerifyProvider); !ok {
		t.Fatal("expected LogVerifyProvider when PNVS env is unset")
	}
}

func TestNewVerifyProviderFromEnv_PNVSWhenConfigured(t *testing.T) {
	t.Setenv("ALIYUN_SMS_ACCESS_KEY_ID", "id")
	t.Setenv("ALIYUN_SMS_ACCESS_KEY_SECRET", "secret")
	t.Setenv("ALIYUN_SMS_SIGN_NAME", "muvee")
	t.Setenv("ALIYUN_SMS_TEMPLATE_CODE", "SMS_1")
	if _, ok := NewVerifyProviderFromEnv().(*PNVSProvider); !ok {
		t.Fatal("expected PNVSProvider when all env vars are set")
	}
}

func TestDomesticPhone(t *testing.T) {
	cases := map[string]string{
		"+8613800138000": "13800138000",
		"+14155552671":   "14155552671",
		"13800138000":    "13800138000",
	}
	for in, want := range cases {
		if got := domesticPhone(in); got != want {
			t.Errorf("domesticPhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGenerateNumericCode(t *testing.T) {
	re := regexp.MustCompile(`^\d{6}$`)
	for i := 0; i < 50; i++ {
		c, err := generateNumericCode()
		if err != nil || !re.MatchString(c) {
			t.Fatalf("bad code %q err=%v", c, err)
		}
	}
}
