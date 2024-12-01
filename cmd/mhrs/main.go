// Package main provides an email sending service that handles outgoing automated emails
// via Gmail SMTP using TLS encryption. It includes logging and error handling.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"mailhubrelay/internal/config"

	"github.com/LixenWraith/logger"
	"github.com/jordan-wright/email"
)

const appName = "mhrs"

// EmailRequest represents the structure of an incoming email sending request
type EmailRequest struct {
	Recipient string `json:"recipient"` // Email address of the recipient
	Subject   string `json:"subject"`   // Subject line of the email
	Body      []byte `json:"body"`      // Body content of the email
}

// main initializes and runs the email service
func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, configExists, err := config.Load(appName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}
	if !configExists {
		if config.Save(cfg, appName) != nil {
			fmt.Fprintf(os.Stderr, "Failed to save configuration: %v\n", err)
		}
	}

	if err := logger.Init(ctx, &cfg.Logging); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Shutdown(ctx)

	logger.Info(ctx, "Starting Mail Hub Relay Service", "listen_addr", cfg.Server.InternalAddr, "smtp_host", cfg.SMTP.Host, "smtp_port", cfg.SMTP.Port)

	// Setup TCP listener
	listener, err := net.Listen("tcp", cfg.Server.InternalAddr)
	if err != nil {
		logger.Error(ctx, "Failed to start TCP listener", "error", err.Error())
		return
	}
	defer listener.Close()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigChan)

	go handleSignals(ctx, cancel, sigChan, cfg)
	go acceptConnections(ctx, listener, cfg)

	<-ctx.Done()

	// Create separate shutdown context
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	logger.Info(shutdownCtx, "Initiating shutdown sequence")
	if err := logger.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintf(os.Stderr, "Shutdown error: %v\n", err)
	}
}

// handleSignals manages system signals for graceful shutdown and configuration reloading.
// It handles SIGHUP for config reload and SIGINT/SIGTERM for graceful shutdown.
func handleSignals(ctx context.Context, cancel context.CancelFunc, sigChan chan os.Signal, cfg *config.Config) {
	logger.Debug(ctx, "Starting signal handler")
	for {
		select {
		case <-ctx.Done():
			return
		case sig := <-sigChan:
			switch sig {
			case syscall.SIGHUP:
				if err := reloadConfig(ctx, cfg); err != nil {
					logger.Error(ctx, "Failed to reload configuration", "error", err)
				}
			case syscall.SIGINT, syscall.SIGTERM:
				logger.Info(ctx, "Received shutdown signal", "signal", sig.String())
				cancel()
				return
			}
		}
	}
}

// reloadConfig reloads the service configuration from disk and reinitializes the logger.
// Returns an error if loading the new configuration or reinitializing the logger fails.
func reloadConfig(ctx context.Context, cfg *config.Config) error {
	newConfig, configExists, err := config.Load(appName)
	if err != nil {
		return fmt.Errorf("failed to load new configuration: %w", err)
	}
	if !configExists {
		return fmt.Errorf("configuration file not found")
	}

	if err := logger.Init(ctx, &newConfig.Logging); err != nil {
		return fmt.Errorf("failed to reinitialize logger: %w", err)
	}

	*cfg = *newConfig
	logger.Info(ctx, "Configuration reloaded successfully")
	return nil
}

// acceptConnections handles incoming TCP connections
func acceptConnections(ctx context.Context, listener net.Listener, cfg *config.Config) {
	logger.Debug(ctx, "Starting connection acceptor")

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				logger.Debug(ctx, "Stopping connection acceptor", "reason", "context cancelled")
				return
			default:
				logger.Error(ctx, "Failed to accept connection", "error", err.Error())
				continue
			}
		}
		go handleConnection(ctx, conn, cfg)
	}
}

// handleConnection processes a single connection and decodes the email request
func handleConnection(ctx context.Context, conn net.Conn, cfg *config.Config) {
	logger.Info(ctx, "New connection received", "remote_addr", conn.RemoteAddr().String())
	defer conn.Close()

	var req EmailRequest
	decoder := json.NewDecoder(conn)

	logger.Debug(ctx, "Decoding email request")
	if err := decoder.Decode(&req); err != nil {
		logger.Error(ctx, "Failed to decode email request", "error", err.Error(), "remote_addr", conn.RemoteAddr().String())
		return
	}

	logger.Debug(ctx, "Successfully decoded email request", "recipient", req.Recipient, "subject_length", len(req.Subject))
	var wg sync.WaitGroup
	wg.Add(1)
	emailCtx, cancel := context.WithTimeout(ctx, cfg.Server.Timeout)
	go func() {
		defer wg.Done()
		defer cancel()
		processEmail(emailCtx, req, cfg)
	}()
	wg.Wait()
}

// processEmail handles the email sending process with retries
func processEmail(ctx context.Context, req EmailRequest, cfg *config.Config) {
	logger.Info(ctx, "Processing email request", "recipient", req.Recipient, "subject", req.Subject)

	e := &email.Email{
		To:      []string{req.Recipient},
		From:    cfg.SMTP.FromAddr,
		Subject: req.Subject,
		Text:    req.Body,
	}

	for attempt := 0; attempt < cfg.Server.MaxRetries; attempt++ {
		logger.Debug(ctx, "Attempting to send email", "attempt", attempt+1, "recipient", req.Recipient)

		if err := sendEmail(ctx, e, cfg); err != nil {
			logger.Error(ctx, "Email attempt failed",
				"attempt", attempt+1,
				"recipient", req.Recipient,
				"error", err,
				"will_retry", attempt < cfg.Server.MaxRetries-1)

			if attempt < cfg.Server.MaxRetries-1 {
				select {
				case <-time.After(cfg.Server.RetryDelay):
					continue
				case <-ctx.Done():
					logger.Debug(ctx, "Email processing cancelled", "reason", "context done")
					return
				}
			}
		} else {
			logger.Info(ctx, "Email sent successfully",
				"recipient", req.Recipient,
				"subject", req.Subject,
				"attempt", attempt+1)
			return
		}
	}
}

// sendEmail performs the actual email sending operation using SMTP
func sendEmail(ctx context.Context, e *email.Email, cfg *config.Config) error {
	logger.Debug(ctx, "Preparing to send email",
		"to", e.To,
		"from", e.From,
		"subject", e.Subject)

	auth := smtp.PlainAuth("", cfg.SMTP.AuthUser, cfg.SMTP.AuthPass, cfg.SMTP.Host)

	tlsConfig := &tls.Config{
		ServerName: cfg.SMTP.Host,
		MinVersion: tls.VersionTLS12,
	}

	logger.Debug(ctx, "Initiating SMTP connection",
		"host", cfg.SMTP.Host,
		"port", cfg.SMTP.Port)

	err := e.SendWithStartTLS(cfg.SMTP.Host+":"+cfg.SMTP.Port, auth, tlsConfig)
	if err != nil {
		logger.Error(ctx, "Failed to send email",
			"error", err.Error(),
			"host", cfg.SMTP.Host,
			"port", cfg.SMTP.Port,
			"recipient", e.To)
		return fmt.Errorf("failed to send email: %w", err)
	}

	logger.Debug(ctx, "Email sent successfully",
		"recipient", e.To,
		"subject", e.Subject)
	return nil
}
