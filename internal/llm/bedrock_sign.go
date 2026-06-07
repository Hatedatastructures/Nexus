package llm

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ── AWS SigV4 简化签名 ─────────────────────────────────────────────────────

// signAWSRequest 对 HTTP 请求进行简化的 AWS SigV4 签名。
// 参考: https://docs.aws.amazon.com/general/latest/gr/signature-version-4.html
func signAWSRequest(req *http.Request, body []byte, accessKey, secretKey, sessionToken, region string) error {
	now := time.Now().UTC()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")
	serviceName := "bedrock"

	// 计算请求体的 SHA256
	bodyHash := sha256Hex(body)

	// 设置必需的 AWS 头
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("Host", req.URL.Host)
	if sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", sessionToken)
	}

	// 构建规范请求
	canonicalHeaders := "content-type:" + req.Header.Get("Content-Type") + "\n" +
		"host:" + req.URL.Host + "\n" +
		"x-amz-date:" + amzDate + "\n"
	if sessionToken != "" {
		canonicalHeaders += "x-amz-security-token:" + sessionToken + "\n"
	}
	signedHeaders := "content-type;host;x-amz-date"
	if sessionToken != "" {
		signedHeaders += ";x-amz-security-token"
	}

	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.Path,
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		bodyHash,
	}, "\n")

	// 构建待签名字符串
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, serviceName)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	// 计算签名
	signingKey := deriveSigningKey(secretKey, dateStamp, region, serviceName)
	signature := hmacSHA256Hex(signingKey, []byte(stringToSign))

	// 设置 Authorization 头
	authorization := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature)
	req.Header.Set("Authorization", authorization)

	return nil
}

// sha256Hex 计算数据的 SHA256 十六进制编码。
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// hmacSHA256Hex 使用 HMAC-SHA256 计算签名。
func hmacSHA256Hex(key, data []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// deriveSigningKey 派生 SigV4 签名密钥。
func deriveSigningKey(secretKey, dateStamp, region, service string) []byte {
	kSecret := []byte("AWS4" + secretKey)
	kDate := hmacSHA256(kSecret, []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	return kSigning
}

// hmacSHA256 计算 HMAC-SHA256 摘要。
func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
