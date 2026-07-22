package sms

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SMSSender delivers a one-time login code to a phone number. Implementations
// must be safe for concurrent use.
type SMSSender interface {
	SendCode(ctx context.Context, phone, code string) error
}

// LogSender is the development fallback used when the Aliyun credentials are
// not fully configured. It logs the code to the server log instead of sending
// a real SMS, so the login flow can be exercised end-to-end locally.
type LogSender struct{}

func (LogSender) SendCode(_ context.Context, phone, code string) error {
	log.Printf("[sms][DEV MODE] verification code for %s = %s "+
		"(set ALIYUN_SMS_* env vars to send real SMS)", phone, code)
	return nil
}

// AliyunSender sends codes via Aliyun's dysmsapi SendSms RPC. The API is a
// single signed HTTP GET, so we implement it directly against net/http rather
// than pulling in the (very large) official SDK dependency tree. The RPC
// signature algorithm (HMAC-SHA1 over the sorted, percent-encoded query) is
// stable and documented at
// https://help.aliyun.com/document_detail/315526.html.
type AliyunSender struct {
	AccessKeyID     string
	AccessKeySecret string
	SignName        string
	TemplateCode    string
	// Endpoint defaults to https://dysmsapi.aliyuncs.com/ when empty; overridable in tests.
	Endpoint   string
	HTTPClient *http.Client
}

// NewSenderFromEnv returns an AliyunSender when all four ALIYUN_SMS_* variables
// are set, otherwise a LogSender. This lets the feature ship before the SMS
// signature/template are approved: fill the env vars later and restart, no code
// change needed.
func NewSenderFromEnv() SMSSender {
	id := os.Getenv("ALIYUN_SMS_ACCESS_KEY_ID")
	secret := os.Getenv("ALIYUN_SMS_ACCESS_KEY_SECRET")
	sign := os.Getenv("ALIYUN_SMS_SIGN_NAME")
	tmpl := os.Getenv("ALIYUN_SMS_TEMPLATE_CODE")
	if id == "" || secret == "" || sign == "" || tmpl == "" {
		log.Printf("[sms] ALIYUN_SMS_* not fully configured; using dev LogSender")
		return LogSender{}
	}
	return &AliyunSender{
		AccessKeyID:     id,
		AccessKeySecret: secret,
		SignName:        sign,
		TemplateCode:    tmpl,
	}
}

func (a *AliyunSender) SendCode(ctx context.Context, phone, code string) error {
	params := map[string]string{
		"AccessKeyId":      a.AccessKeyID,
		"Action":           "SendSms",
		"Format":           "JSON",
		"RegionId":         "cn-hangzhou",
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureNonce":   uuid.NewString(),
		"SignatureVersion": "1.0",
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"Version":          "2017-05-25",
		"PhoneNumbers":     aliyunPhone(phone),
		"SignName":         a.SignName,
		"TemplateCode":     a.TemplateCode,
		"TemplateParam":    fmt.Sprintf(`{"code":"%s"}`, code),
	}
	params["Signature"] = signAliyun(http.MethodGet, params, a.AccessKeySecret)

	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	endpoint := a.Endpoint
	if endpoint == "" {
		endpoint = "https://dysmsapi.aliyuncs.com/"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	client := a.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var out struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("aliyun sms: unexpected response (%d): %s", resp.StatusCode, string(body))
	}
	if out.Code != "OK" {
		return fmt.Errorf("aliyun sms: %s: %s", out.Code, out.Message)
	}
	return nil
}

// aliyunPhone converts an E.164 number to the form Aliyun expects: a domestic
// Chinese number is sent as bare 11 digits; anything else keeps its digits
// without the leading '+'.
func aliyunPhone(e164 string) string {
	if strings.HasPrefix(e164, "+86") {
		return e164[3:]
	}
	return strings.TrimPrefix(e164, "+")
}

// signAliyun computes the Aliyun RPC signature over the request parameters.
func signAliyun(method string, params map[string]string, secret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var canonical strings.Builder
	for i, k := range keys {
		if i > 0 {
			canonical.WriteByte('&')
		}
		canonical.WriteString(percentEncode(k))
		canonical.WriteByte('=')
		canonical.WriteString(percentEncode(params[k]))
	}
	stringToSign := method + "&" + percentEncode("/") + "&" + percentEncode(canonical.String())

	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// percentEncode applies Aliyun's required RFC 3986 encoding on top of
// url.QueryEscape.
func percentEncode(s string) string {
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")
	e = strings.ReplaceAll(e, "*", "%2A")
	e = strings.ReplaceAll(e, "%7E", "~")
	return e
}
