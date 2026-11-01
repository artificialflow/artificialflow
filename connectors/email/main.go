package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/artificialflow/artificialflow/connectors/internal/common"
)

const jobType = "io.artificialflow.connector.email"

func main() {
	baseURL := common.EnvOr("ARTIFICIALFLOW_BASE_URL", "http://localhost:9100/api")
	token := os.Getenv("ARTIFICIALFLOW_TOKEN")

	runtime, err := common.NewRuntime(common.RuntimeConfig{
		BaseURL: baseURL, Token: token, JobType: jobType,
		WorkerName: common.EnvOr("WORKER_NAME", "email-connector"),
		MaxJobs:    3, LockDuration: 60 * time.Second,
		Execute: func(_ context.Context, inputs map[string]any) (map[string]any, error) {
			return sendEmail(inputs)
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("email connector listening for %s", jobType)
	if err := runtime.Run(ctx); err != nil && err != context.Canceled {
		log.Fatal(err)
	}
}

func sendEmail(vars map[string]any) (map[string]any, error) {
	host := common.EnvOr("SMTP_HOST", "")
	port := common.EnvOr("SMTP_PORT", "587")
	user := os.Getenv("SMTP_USERNAME")
	pass := os.Getenv("SMTP_PASSWORD")
	from := common.EnvOr("SMTP_FROM", user)
	if host == "" || from == "" {
		return nil, fmt.Errorf("SMTP_HOST and SMTP_FROM (or SMTP_USERNAME) are required")
	}
	to := common.StringVar(vars, "emailTo")
	if to == "" {
		to = common.StringVar(vars, "to")
	}
	subject := common.StringVar(vars, "emailSubject")
	if subject == "" {
		subject = common.StringVar(vars, "subject")
	}
	body := common.StringVar(vars, "emailBody")
	if body == "" {
		body = common.StringVar(vars, "body")
	}
	if to == "" || subject == "" {
		return nil, fmt.Errorf("emailTo and emailSubject are required")
	}
	addr := net.JoinHostPort(host, port)
	msg := []byte("From: " + from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" +
		body + "\r\n")
	var auth smtp.Auth
	if user != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}
	if err := smtp.SendMail(addr, auth, from, splitAddresses(to), msg); err != nil {
		return nil, err
	}
	return map[string]any{
		"emailTo":      to,
		"emailSubject": subject,
		"emailSent":    true,
	}, nil
}

func splitAddresses(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
