// Package main implements a sendmail-compatible command line interface that forwards
// email requests to MHRS. It supports standard sendmail command line options while
// internally routing all emails through the Mail Hub Relay Server.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"mailhubrelay/internal/config"
)

const appName = "mhrc"

// Exit codes following FreeBSD's sendmail conventions
const (
	EX_OK          = 0  // Successful completion
	EX_USAGE       = 64 // Command line usage error
	EX_NOUSER      = 67 // Recipient address invalid
	EX_UNAVAILABLE = 69 // Service unavailable
	EX_TEMPFAIL    = 75 // Temporary failure
)

type EmailRequest struct {
	Recipient string `json:"recipient"`
	Subject   string `json:"subject"`
	Body      []byte `json:"body"`
}

// EmailMessage represents a parsed email with headers and body
// Used internally to process input before sending to MHRS
type EmailMessage struct {
	headers map[string]string
	body    *bytes.Buffer
}

func main() {
	var (
		_          = flag.String("f", "", "from address ") // fromAddr: (ignored, always uses mhrs default sender)
		useHeaders = flag.Bool("t", false, "extract recipients from message headers")
		ignoreDots = flag.Bool("i", false, "ignore dots alone on lines")
		subject    = flag.String("s", "", "specify subject")
		bpFlag     = flag.Bool("bp", false, "print mail queue (disabled)")
		biFlag     = flag.Bool("bi", false, "initialize aliases (disabled)")
		bhFlag     = flag.Bool("bh", false, "print persistent host status (disabled)")
		bpurgFlag  = flag.Bool("bpurg", false, "purge host status (disabled)")
	)

	flag.Parse()

	cfg, _, err := config.Load(appName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(EX_UNAVAILABLE)
	}

	fmt.Println(cfg)

	switch {
	case *bpFlag || *biFlag || *bhFlag || *bpurgFlag:
		fmt.Println("Mail queue is empty")
		os.Exit(EX_OK)
	}

	msg, err := parseMessage(os.Stdin, *ignoreDots)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading message: %v\n", err)
		os.Exit(EX_USAGE)
	}

	// Determine recipient
	var recipient string
	if *useHeaders {
		recipient = msg.headers["To"]
		if recipient == "" {
			fmt.Fprintln(os.Stderr, "No recipient specified in headers")
			os.Exit(EX_NOUSER)
		}
	} else if len(flag.Args()) > 0 {
		recipient = flag.Arg(0)
	} else {
		fmt.Fprintln(os.Stderr, "No recipient specified")
		os.Exit(EX_USAGE)
	}

	// Build email request
	emailSubject := *subject
	if emailSubject == "" {
		emailSubject = msg.headers["Subject"]
	}
	if emailSubject == "" {
		emailSubject = "Message from mhrc"
	}

	// Trim any trailing newline from body
	bodyBytes := bytes.TrimRight(msg.body.Bytes(), "\n")

	req := EmailRequest{
		Recipient: recipient,
		Subject:   emailSubject,
		Body:      bodyBytes, // msg.body.Bytes(),
	}

	if err := sendToMHRS(req, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error sending email: %v\n", err)
		os.Exit(EX_TEMPFAIL)
	}

	os.Exit(EX_OK)
}

// parseMessage reads and parses an email message from stdin
// Supports standard sendmail input format with optional dot-termination
func parseMessage(r io.Reader, ignoreDots bool) (*EmailMessage, error) {
	msg := &EmailMessage{
		headers: make(map[string]string),
		body:    new(bytes.Buffer),
	}

	scanner := bufio.NewScanner(r)
	inHeaders := true

	for scanner.Scan() {
		line := scanner.Text()

		if inHeaders {
			if line == "" {
				inHeaders = false
				continue
			}

			if strings.Contains(line, ":") {
				parts := strings.SplitN(line, ":", 2)
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				msg.headers[key] = value
			}
			continue
		}

		if !ignoreDots && line == "." {
			break
		}

		msg.body.WriteString(line)
		msg.body.WriteString("\n")
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return msg, nil
}

// sendToMHRS forwards an email request to the Mail Hub Relay Server over TCP.
// It establishes a connection with timeout, marshals the request to JSON, and sends the data.
// Returns an error if connection, marshaling, or sending fails.
func sendToMHRS(req EmailRequest, cfg *config.Config) error {
	dialer := net.Dialer{
		Timeout: 30 * time.Second,
	}

	conn, err := dialer.Dial("tcp", cfg.Server.InternalAddr)
	if err != nil {
		return fmt.Errorf("error connecting to MHRS: %w", err)
	}
	defer conn.Close()

	// Set deadline for the write operation
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fmt.Errorf("error setting write deadline: %w", err)
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("error creating JSON: %w", err)
	}

	jsonData = append(jsonData, '\n')

	if _, err := conn.Write(jsonData); err != nil {
		return fmt.Errorf("error sending data: %w", err)
	}

	return nil
}
