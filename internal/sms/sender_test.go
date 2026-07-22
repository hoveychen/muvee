package sms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLogSender_NeverErrors(t *testing.T) {
	if err := (LogSender{}).SendCode(context.Background(), "+8613800138000", "123456"); err != nil {
		t.Fatalf("LogSender.SendCode error: %v", err)
	}
}

func TestNewSenderFromEnv_FallsBackToLog(t *testing.T) {
	// None of the ALIYUN_SMS_* vars set in the test env → dev LogSender.
	t.Setenv("ALIYUN_SMS_ACCESS_KEY_ID", "")
	t.Setenv("ALIYUN_SMS_ACCESS_KEY_SECRET", "")
	t.Setenv("ALIYUN_SMS_SIGN_NAME", "")
	t.Setenv("ALIYUN_SMS_TEMPLATE_CODE", "")
	if _, ok := NewSenderFromEnv().(LogSender); !ok {
		t.Fatal("expected LogSender when Aliyun env is unset")
	}
}

func TestNewSenderFromEnv_PartialConfigStillFallsBack(t *testing.T) {
	t.Setenv("ALIYUN_SMS_ACCESS_KEY_ID", "id")
	t.Setenv("ALIYUN_SMS_ACCESS_KEY_SECRET", "secret")
	t.Setenv("ALIYUN_SMS_SIGN_NAME", "") // missing → must not build AliyunSender
	t.Setenv("ALIYUN_SMS_TEMPLATE_CODE", "SMS_1")
	if _, ok := NewSenderFromEnv().(LogSender); !ok {
		t.Fatal("expected LogSender when config is incomplete")
	}
}

func TestAliyunSender_SendCode(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"ok", `{"Code":"OK","Message":"OK"}`, false},
		{"throttled", `{"Code":"isv.BUSINESS_LIMIT_CONTROL","Message":"limit"}`, true},
		{"garbage", `not json`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotSig, gotPhone string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotSig = r.URL.Query().Get("Signature")
				gotPhone = r.URL.Query().Get("PhoneNumbers")
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			s := &AliyunSender{
				AccessKeyID:     "id",
				AccessKeySecret: "secret",
				SignName:        "muvee",
				TemplateCode:    "SMS_1",
				Endpoint:        srv.URL + "/",
				HTTPClient:      srv.Client(),
			}
			err := s.SendCode(context.Background(), "+8613800138000", "123456")
			if tt.wantErr != (err != nil) {
				t.Fatalf("SendCode err=%v, wantErr=%v", err, tt.wantErr)
			}
			if gotSig == "" {
				t.Error("request carried no Signature")
			}
			if gotPhone != "13800138000" {
				t.Errorf("PhoneNumbers = %q, want bare CN 13800138000", gotPhone)
			}
		})
	}
}

func TestSignAliyun_Deterministic(t *testing.T) {
	p := map[string]string{"Action": "SendSms", "Timestamp": "2026-01-01T00:00:00Z"}
	a := signAliyun("GET", p, "secret")
	b := signAliyun("GET", p, "secret")
	if a == "" || a != b {
		t.Fatalf("signature not deterministic: %q vs %q", a, b)
	}
}

func TestPercentEncode(t *testing.T) {
	cases := map[string]string{
		"a b":  "a%20b",
		"a*b":  "a%2Ab",
		"a~b":  "a~b",
		"a/b":  "a%2Fb",
		"a=b&": "a%3Db%26",
	}
	for in, want := range cases {
		if got := percentEncode(in); got != want {
			t.Errorf("percentEncode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAliyunPhone(t *testing.T) {
	cases := map[string]string{
		"+8613800138000": "13800138000",
		"+14155552671":   "14155552671",
	}
	for in, want := range cases {
		if got := aliyunPhone(in); got != want {
			t.Errorf("aliyunPhone(%q) = %q, want %q", in, got, want)
		}
	}
}
