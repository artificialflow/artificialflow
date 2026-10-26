package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/artificialflow/artificialflow/connectors/internal/common"
)

const jobType = "io.artificialflow.connector.s3"

func main() {
	baseURL := common.EnvOr("ARTIFICIALFLOW_BASE_URL", "http://localhost:9100/api")
	token := os.Getenv("ARTIFICIALFLOW_TOKEN")

	runtime, err := common.NewRuntime(common.RuntimeConfig{
		BaseURL: baseURL, Token: token, JobType: jobType,
		WorkerName: common.EnvOr("WORKER_NAME", "s3-connector"),
		MaxJobs:    3, LockDuration: 120 * time.Second,
		Execute: putObject,
	})
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("s3 connector listening for %s", jobType)
	if err := runtime.Run(ctx); err != nil && err != context.Canceled {
		log.Fatal(err)
	}
}

// putObject uploads via S3-compatible SigV4 PUT (AWS or MinIO).
func putObject(ctx context.Context, vars map[string]any) (map[string]any, error) {
	endpoint := common.EnvOr("S3_ENDPOINT", "")
	region := common.EnvOr("S3_REGION", "us-east-1")
	accessKey := os.Getenv("S3_ACCESS_KEY")
	secretKey := os.Getenv("S3_SECRET_KEY")
	bucket := common.StringVar(vars, "s3Bucket")
	if bucket == "" {
		bucket = common.EnvOr("S3_BUCKET", "")
	}
	key := common.StringVar(vars, "s3Key")
	body := common.StringVar(vars, "s3Body")
	if body == "" {
		body = common.StringVar(vars, "body")
	}
	if endpoint == "" || accessKey == "" || secretKey == "" || bucket == "" || key == "" {
		return nil, fmt.Errorf("S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY, s3Bucket, and s3Key are required")
	}
	contentType := common.StringVar(vars, "contentType")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	u, err := url.Parse(strings.TrimRight(endpoint, "/") + "/" + bucket + "/" + strings.TrimPrefix(key, "/"))
	if err != nil {
		return nil, err
	}
	payload := []byte(body)
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadHash := sha256Hex(payload)
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n", u.Host, payloadHash, amzDate)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalRequest := strings.Join([]string{
		"PUT",
		u.EscapedPath(),
		"",
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	credentialScope := dateStamp + "/" + region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signingKey := awsSignKey(secretKey, dateStamp, region, "s3")
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))
	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Host", u.Host)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("Authorization", authHeader)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("s3 put failed: %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return map[string]any{
		"s3Bucket":   bucket,
		"s3Key":      key,
		"s3Status":   resp.StatusCode,
		"s3Uploaded": true,
	}, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write(data)
	return h.Sum(nil)
}

func awsSignKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}
