# MailHubRelay

## System Architecture Overview

MailHubRelay implements a distributed email handling architecture designed to facilitate the conversion and transmission
of various input formats into Gmail-compatible email communications. The system comprises three primary components with
complementary utility scripts for deployment management.

## Core Components

### MHRS (Mail Hub Relay Server)

The central relay server component operates as a standalone service or foreground application, implementing the
following functionalities:

- JSON-format email request processing via localhost:2525 (configurable)
- Secure email transmission through Gmail SMTP with TLS encryption
- Comprehensive logging capabilities via the logger package
- Configurable retry mechanisms for enhanced delivery reliability
- External configuration support for deployment flexibility

### MHRC (Mail Hub Relay Client)

A command-line interface implementing sendmail-compatible operations:

- Direct compatibility with standard sendmail command-line parameters
- Internal routing through MHRS for standardized email delivery
- Foreground operation mode for immediate feedback
- Configuration inheritance from system-wide settings

### SubmitF (Submit Form Handler)

Web service implementation for form submission processing:

- HTTP endpoint exposure on port 8845 (configurable)
- CORS-compatible security framework
- JSON request handling with validation
- Configurable operation modes: service or foreground application

## Technical Requirements

### System Prerequisites

- Gmail account with domain verification
- Configured sender email alias
- Application-specific password (Google Account > Security > App Passwords)
- TLS-enabled communication capability

### Network Configuration

- Outbound access to port 587 (Gmail SMTP)
- Local port 2525 (MHRS internal communication)
- Port 8845 (SubmitF form submission handling)
- Appropriate firewall rules for specified ports

## Build

Clone or fork the repository.

Use standard go build process, or use make.sh script to with pre-defined make config for freebsd/amd64 build:

```bash
# Build script use for FreeBSD build
./script/make.sh -c mhrs.make   # Mail Hub Relay Server
./script/make.sh -c mhrc.make   # Mail Hub Relay Client
./script/make.sh -c submitf.make # Submit Form Handler
```

## Installation

### Service Installation

The installation/uninstallation scripts facilitate deployment.

```bash
./install-service.sh [-r] [-l] [-c] <path_to_executable>
./uninstall-service.sh [-l] [-c] <service_name>
```

Parameters:

- `-r`: Execute service with root privileges
- `-l`: Create and preserve of log directory
- `-c`: Create and preserve of configuration directory

Deployment example:

```bash
./scripts/install-service.sh mhrs    # Mail Hub Relay Server
./scripts/install-service.sh submitf # Form Handler
./scripts/install-app.sh mhrc        # Mail Hub Relay Client
```

### Configuration Management

Default configuration paths (not configurable):

- `/usr/local/etc/<service_name>/<service_name>.toml`
- Service-specific directories created automatically
- Default values provided if configuration absent

## Implementation Guidelines

### MHRC FreeBSD Sendmail Integration

1. Mailer Configuration Update:

```bash
# Edit /etc/mail/mailer.conf and /usr/local/etc/mail/mailer.conf
sendmail        /usr/local/bin/mhrc
send-mail       /usr/local/bin/mhrc
mailq           /usr/local/bin/mhrc -bp
newaliases      /usr/local/bin/mhrc -bi
hoststat        /usr/local/bin/mhrc -bh
purgestat       /usr/local/bin/mhrc -bp
```

2. Sendmail Service Management:

```bash
sysrc sendmail_enable="NO"
sysrc sendmail_submit_enable="NO"
sysrc sendmail_outbound_enable="NO"
sysrc sendmail_msp_queue_enable="NO"
```

3. System Integration:

```bash
ln -s /usr/local/bin/mhrc /usr/local/sbin/sendmail
ln -s /usr/local/bin/mhrc /usr/sbin/sendmail
```

## Operational Usage

Suitable for small-medium servers with low email traffic.

### Flows

```
                                                                 Gmail SMTP
                                                                     |
................................................. Internet ..................
                |            ^            ^                          ^
                v            v            |                          v
................................................ [Firewall] .................
                |            ^            ^                          ^
                v            v            |                          |
......................................................:SSL [NGINX]   |:TLS
                |            ^            ^                          |
                v            v            |                          v
 [MHRC]     [SubmitF]   [Local Apps]  [Website]                    [MHRS]
   |            |            |                                       ^
   v            v            v                                       |
   ------------------------------------------------------------------- 
```

Data Flow Patterns:

A. Web Form Submission Path:
Website form submission -> NGINX -> SubmitF -> MHRS -> Gmail SMTP

B. Local Application Path:
Local Apps -> MHRS -> Gmail SMTP

C. Legacy Application Path:
Sendmail Apps -> MHRC -> MHRS -> Gmail SMTP

### Server Operation (MHRS)

Foreground execution:

```bash
mhrs
```

### Client Implementation (MHRC)

Standard sendmail syntax support:

```bash
# Direct recipient specification
echo "Message content" | mhrc user@example.com

# Header-based routing
echo -e "To: user@example.com\nSubject: Test\n\nMessage" | mhrc -t

# Subject specification
echo "Message content" | mhrc -s "Subject" user@example.com
```

### Form Handler Operation (SubmitF)

Foreground execution:

```bash
submitf
```

### Web Server Integration

nginx configuration example:

```nginx
# Contact form submission endpoint with rate limiting
location /submit {
    proxy_pass http://localhost:8845;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header Host $host;
    limit_req zone=one burst=5 nodelay;
}
```

### Client-Side Integration

```javascript
async function submitForm(event) {
    event.preventDefault();

    const formData = {
        name: document.getElementById('name').value,
        email: document.getElementById('email').value,
        message: document.getElementById('message').value
    };

    try {
        const response = await fetch('http://localhost:8845/submit', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify(formData)
        });

        if (!response.ok) {
            throw new Error('Submission failed');
        }

        alert('Form submitted successfully');
    } catch (error) {
        console.error('Error:', error);
        alert('Failed to submit form');
    }
}
```

## Context & Background

This project emerged from challenges in setting up mail servers in cloud environments. After experimenting with various solutions, several issues became apparent:

- Port 25 is blocked by major cloud providers, forcing reliance on relay services
- Traditional mail servers are complex to configure and tend to fallback to port 25
- Existing lightweight alternatives lacked features, or had dependencies and complexities unsuitable for simple automated email needs

MailHubRelay offers a streamlined solution by acting as an aggregator and mail agent for automated emails, relaying them through Gmail's SMTP service.

## License

MIT license