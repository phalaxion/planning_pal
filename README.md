# Planning Poker — Minimal self-hosted server

## Quick start (local development)

Prereqs: Go 1.22+

Run:

```bash
make run
```

Open: http://localhost:8080/

WebSocket endpoint: `ws://localhost:8080/ws?room=ROOMID&name=NAME`

### Tooling

- `tools/wsmon` — small CLI monitor to print incoming messages from the server:

```bash
go run ./tools/wsmon -room=TEST -name=Alice
```

## Deployment

### Data Store Setup

Create a user to manage the service and own the store data
```bash
sudo adduser --system --no-create-home planning-pal
```

Create and set permissions on the store folder
```bash
sudo mkdir -p /var/lib/planning-pal
sudo chmod -R +x /opt/planning-pal/
sudo chown -R planning-pal /var/lib/planning-pal
```

### Apache Setup

Enable required modules:
```bash
a2enmod proxy proxy_http proxy_wstunnel rewrite
systemctl restart apache2
```

Virtual host config:
```apache

<VirtualHost *:80>
    ServerName planning.domain.com
    Redirect permanent / https://planning.domain.com/
</VirtualHost>

<VirtualHost *:443>
    ServerName planning.domain.com
	ErrorLog ${APACHE_LOG_DIR}/planning_error.log
    CustomLog ${APACHE_LOG_DIR}/planning_access.log combined
	
    # Proxy WebSocket connections to Go
    RewriteEngine On
    RewriteCond %{HTTP:Upgrade} =websocket [NC]
    RewriteRule ^/ws$ ws://localhost:8080/ws [P,L]

    ProxyPreserveHost On
    ProxyPass / http://localhost:8080/
    ProxyPassReverse / http://localhost:8080/

    # Forward real IP
    RequestHeader set X-Forwarded-Proto "https"
    RequestHeader set X-Real-IP %{REMOTE_ADDR}s
	
	SSLEngine On
    SSLCertificateFile /etc/ssl/wildcard.domain.com/wildcard.domain.com.crt
    SSLCertificateKeyFile /etc/ssl/wildcard.domain.com/wildcard.domain.com.key
	SSLCertificateChainFile /etc/ssl/wildcard.domain.com/wildcard.domain.com.intermediate
    SetEnvIf Authorization "(.*)" HTTP_AUTHORIZATION=$1
</VirtualHost>
```

### systemd Service

```ini
# /etc/systemd/system/planning-pal.service
[Unit]
Description=Planning Pal Server
After=network.target

[Service]
User=planning-pal
ExecStart=/opt/planning-pal/server
WorkingDirectory=/opt/planning-pal
Restart=always
RestartSec=3
Environment=STATIC_PATH=/var/www/planning-pal
Environment=PPAL_STORE_PATH=/var/lib/planning-pal
Environment=PPAL_STORE_TYPE=sqlite

[Install]
WantedBy=multi-user.target
```

```bash
systemctl enable planning-pal
systemctl start planning-pal
```

### File Locations

```
/var/www/planning-pal/   ← Apache serves this (frontend static files)
  core/
  lobby/
  ...

/opt/planning-pal/       ← Go binary
  server
```

### Build and Deploy

```bash
make build GOOS=linux

make deploy GOOS=linux
```