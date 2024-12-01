// Package main implements a web service that handles contact form submissions by receiving
// HTTP POST requests and forwarding them to MHRS (Mail Hub Relay Server) for email delivery.
// It acts as a secure intermediary between web forms and the email relay system.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"mailhubrelay/internal/config"

	"github.com/LixenWraith/logger"
)

const appName = "submitf"

// FormData defines the expected structure of incoming form submissions
// All fields are required and validated before processing
type FormData struct {
	Name    string `json:"name"`    // Sender's name
	Email   string `json:"email"`   // Sender's email address (not From email address)
	Message string `json:"message"` // Content of the message
}

// EmailRequest represents the format expected by MHRS
type EmailRequest struct {
	Recipient string `json:"recipient"`
	Subject   string `json:"subject"`
	Body      []byte `json:"body"`
}

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

	fmt.Println(cfg)

	if err := logger.Init(ctx, &cfg.Logging); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Shutdown(ctx)

	logger.Info(ctx, "Starting submitf service", "addr", cfg.Server.ExternalAddr)

	// Handle shutdown gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	server := &http.Server{
		Addr:         cfg.Server.ExternalAddr,
		Handler:      handleSubmit(ctx, cfg),
		ReadTimeout:  cfg.Server.Timeout,
		WriteTimeout: cfg.Server.Timeout,
	}

	go func() {
		<-sigChan
		logger.Info(ctx, "Shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		logger.Info(shutdownCtx, "Shutdown signal received")

		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error(shutdownCtx, "Server shutdown error", "error", err)
		}

		if err := logger.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintf(os.Stderr, "Logger shutdown error: %v\n", err)
		}
	}()

	logger.Info(ctx, "Server started", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error(ctx, "Server error", "error", err)
		os.Exit(1)
	}
}

// handleSubmit returns an http.HandlerFunc that processes form submissions
// It implements CORS protection and validates form data before forwarding to MHRS
func handleSubmit(ctx context.Context, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Debug(ctx, "Handling new submission request", "method", r.Method, "remote_addr", r.RemoteAddr)

		// Set CORS headers
		origin := r.Header.Get("Origin")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")

		// Check if origin is allowed
		originAllowed := false
		for _, allowed := range cfg.Server.AllowedOrigins {
			if origin == allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				originAllowed = true
				break
			}
		}

		// handle preflight
		if r.Method == http.MethodOptions {
			if originAllowed {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusForbidden)
			}
			return
		}

		// Continue only if origin is allowed
		if !originAllowed {
			logger.Warn(ctx, "Invalid origin", "origin", origin)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		if r.Method != http.MethodPost {
			logger.Warn(ctx, "Invalid request method", "method", r.Method)
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var form FormData
		if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
			logger.Error(ctx, "Failed to decode request body", "error", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		logger.Debug(ctx, "Received form submission",
			"name", form.Name,
			"email", form.Email,
			"message_length", len(form.Message))

		if err := validateForm(form); err != nil {
			logger.Error(ctx, "Form validation failed", "error", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := sendToMHRS(ctx, form, cfg); err != nil {
			logger.Error(ctx, "Failed to send to MHRS", "error", err)
			http.Error(w, "Failed to process submission", http.StatusInternalServerError)
			return
		}

		logger.Info(ctx, "Form submission processed successfully",
			"name", form.Name,
			"email", form.Email)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}
}

// validateForm performs basic validation of form submission data
// Returns error if any required field is missing or invalid
func validateForm(form FormData) error {
	if strings.TrimSpace(form.Name) == "" {
		return errors.New("name is required")
	}
	if !strings.Contains(form.Email, "@") {
		return errors.New("invalid email address")
	}
	if strings.TrimSpace(form.Message) == "" {
		return errors.New("message is required")
	}
	return nil
}

// sendToMHRS forwards validated form data to MHRS over localhost TCP connection
// Formats the email and handles the connection with configurable timeout
func sendToMHRS(ctx context.Context, form FormData, cfg *config.Config) error {
	logger.Debug(ctx, "Preparing email request for MHRS")

	emailBody := formatEmailBody(form)
	req := EmailRequest{
		Recipient: cfg.SMTP.FromAddr,
		Subject:   "Contact Form Submission from " + form.Name,
		Body:      []byte(emailBody),
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return err
	}

	logger.Debug(ctx, "Connecting to MHRS", "size", len(jsonData))

	conn, err := net.Dial("tcp", cfg.Server.InternalAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Set write deadline
	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		logger.Error(ctx, "Failed to set write deadline", "error", err)
		return err
	}

	if _, err := conn.Write(jsonData); err != nil {
		logger.Error(ctx, "Failed to write to MHRS", "error", err)
		return err
	}

	logger.Info(ctx, "Email request sent to MHRS",
		"recipient", req.Recipient,
		"subject", req.Subject)
	return nil
}

// formatEmailBody constructs a formatted email message string from the form submission data.
// It includes the sender's name, email address, and their message in a readable format.
func formatEmailBody(form FormData) string {
	return "New contact form submission:\n\n" +
		"Name: " + form.Name + "\n" +
		"Email: " + form.Email + "\n\n" +
		"Message:\n" + form.Message
}
