# Dota Inhouse Migration Guide

This guide is for migrating the dota-inhouse project to a new PC on the same local network.

## Prerequisites on New Machine

1. **Install Go**: Version 1.21 or higher
   ```bash
   # Check Go version
   go version
   ```

2. **Install system dependencies**:
   ```bash
   # For Discord bot (if using)
   sudo apt-get install -y build-essential
   ```

## Step 1: Transfer Project Files

From the **old machine** (current: macOS at `/Users/edvart/Programming/dota-inhouse`):

```bash
# Replace NEW_MACHINE_IP with the actual IP address of the new Linux machine
# Replace USERNAME with your username on the new machine
# Replace /path/to/destination with where you want the project on the new machine

rsync -avz --progress \
  --exclude 'data/' \
  --exclude '.git/' \
  /Users/edvart/Programming/dota-inhouse/ \
  USERNAME@NEW_MACHINE_IP:/path/to/destination/dota-inhouse/
```

**Important exclusions:**
- `data/` - Database and logs (migrate separately if needed)
- `.git/` - Git history (clone fresh or include if needed)

### To transfer database separately:
```bash
rsync -avz --progress \
  /Users/edvart/Programming/dota-inhouse/data/ \
  USERNAME@NEW_MACHINE_IP:/path/to/destination/dota-inhouse/data/
```

## Step 2: Set Up Environment Variables

On the **new machine**, copy and configure `set_env.sh`:

```bash
cd /path/to/destination/dota-inhouse
cp set_env.sh.example set_env.sh  # If example exists, otherwise create from scratch
chmod +x set_env.sh
nano set_env.sh
```

Required variables:
```bash
export PORT=8080
export BASE_URL="https://yourdomain.com"  # Update for production
export STEAM_API_KEY="your_steam_api_key"
export DATABASE_PATH="./data/inhouse.db"
export LOG_PATH="./data/inhouse.log"
export ADMIN_STEAM_IDS="comma,separated,steam,ids"

# Push notifications (optional)
export VAPID_PUBLIC_KEY="your_vapid_public_key"
export VAPID_PRIVATE_KEY="your_vapid_private_key"
export VAPID_SUBJECT="mailto:your@email.com"

# Discord bot (optional)
export DISCORD_BOT_TOKEN="your_discord_bot_token"
export DISCORD_CHANNEL_ID="your_discord_channel_id"

# Dota 2 bots (optional, for lobby creation)
export BOT1_USERNAME="bot1_username"
export BOT1_PASSWORD="bot1_password"
# BOT2, BOT3 if needed
```

## Step 3: Build and Run

```bash
# Install dependencies
go mod download

# Build
go build -o bin/server ./cmd/server

# Run
source set_env.sh
./bin/server
```

Or use the dev mode for testing:
```bash
source set_env.sh
export DEV_MODE=true
go run ./cmd/server
```

## Step 4: Migrate Cloudflare Setup

**Your current setup uses Cloudflare.** Choose based on whether you're using Tunnel or traditional DNS:

### Path A: If Using Cloudflare Tunnel (Transfer Existing Tunnel)

1. **On old machine** (192.168.3.9), locate tunnel config:
   ```bash
   ls -la ~/.cloudflared/
   cat ~/.cloudflared/config.yml
   # Note the tunnel ID shown in config
   ```

2. **Copy tunnel to new machine**:
   ```bash
   # From old machine (192.168.3.9)
   rsync -avz ~/.cloudflared/ USERNAME@NEW_MACHINE_IP:~/.cloudflared/
   ```

3. **On new machine**, install cloudflared:
   ```bash
   wget -q https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb
   sudo dpkg -i cloudflared-linux-amd64.deb
   ```

4. **Verify/update config** (`~/.cloudflared/config.yml`):
   ```yaml
   tunnel: YOUR_EXISTING_TUNNEL_ID
   credentials-file: /home/USERNAME/.cloudflared/YOUR_TUNNEL_ID.json

   ingress:
     - hostname: yourdomain.com
       service: http://localhost:8080
     - service: http_status:404
   ```

5. **Test tunnel locally** (before making it permanent):
   ```bash
   cloudflared tunnel run
   # Leave running, test site in browser
   # Ctrl+C to stop when verified
   ```

6. **Set up as systemd service**:
   ```bash
   sudo cloudflared service install
   sudo systemctl enable cloudflared
   sudo systemctl start cloudflared
   sudo systemctl status cloudflared
   ```

7. **Stop old tunnel** (on old machine 192.168.3.9):
   ```bash
   # If running as service:
   sudo systemctl stop cloudflared
   sudo systemctl disable cloudflared
   # If running manually, just Ctrl+C
   ```

### Path B: If Using Traditional DNS (Update A Record)

1. **On new machine**, install nginx:
   ```bash
   sudo apt-get update
   sudo apt-get install -y nginx
   ```

2. **Configure nginx** (`/etc/nginx/sites-available/dota-inhouse`):
   ```nginx
   server {
       listen 80;
       server_name yourdomain.com;

       location / {
           proxy_pass http://localhost:8080;
           proxy_http_version 1.1;
           proxy_set_header Upgrade $http_upgrade;
           proxy_set_header Connection 'upgrade';
           proxy_set_header Host $host;
           proxy_set_header X-Real-IP $remote_addr;
           proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
           proxy_set_header X-Forwarded-Proto $scheme;
       }

       # SSE - disable buffering
       location /events {
           proxy_pass http://localhost:8080;
           proxy_http_version 1.1;
           proxy_set_header Connection '';
           proxy_buffering off;
           proxy_cache off;
           chunked_transfer_encoding off;
           proxy_read_timeout 86400s;
       }
   }
   ```

3. **Enable nginx**:
   ```bash
   sudo ln -s /etc/nginx/sites-available/dota-inhouse /etc/nginx/sites-enabled/
   sudo nginx -t
   sudo systemctl enable nginx
   sudo systemctl start nginx
   ```

4. **Get new machine's public IP**:
   ```bash
   curl ifconfig.me
   # Note the IP address
   ```

5. **Update Cloudflare DNS** (Cloudflare Dashboard):
   - Go to DNS settings
   - Update A record: `yourdomain.com` → `NEW_PUBLIC_IP`
   - Keep proxy enabled (orange cloud)
   - SSL/TLS mode: Full (strict)
   - Propagation: 1-5 minutes

6. **Update port forwarding** (if behind router):
   - Forward port 80 → new machine local IP
   - Forward port 443 → new machine local IP

### Migration Strategy (Zero Downtime)

1. **Prepare new machine** completely (Steps 1-3, Step 5)
2. **Test locally**: `curl http://localhost:8080`
3. **Switch Cloudflare** (tunnel credentials or DNS record)
4. **Verify** via domain name
5. **Monitor** for 15 minutes
6. **Stop old machine** services only after confirmed working

## Step 5: Systemd Service (Optional but Recommended)

Create `/etc/systemd/system/dota-inhouse.service`:

```ini
[Unit]
Description=Dota Inhouse Matchmaking
After=network.target

[Service]
Type=simple
User=USERNAME
WorkingDirectory=/path/to/destination/dota-inhouse
EnvironmentFile=/path/to/destination/dota-inhouse/set_env.sh
ExecStart=/path/to/destination/dota-inhouse/bin/server
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Enable and start:
```bash
sudo systemctl daemon-reload
sudo systemctl enable dota-inhouse
sudo systemctl start dota-inhouse
sudo systemctl status dota-inhouse
```

## Verification Checklist

- [ ] Project files transferred
- [ ] Go dependencies installed (`go mod download`)
- [ ] Environment variables configured in `set_env.sh`
- [ ] Database file exists at `./data/inhouse.db` (or fresh DB created)
- [ ] Server builds successfully (`go build -o bin/server ./cmd/server`)
- [ ] Server runs and listens on configured port
- [ ] Cloudflare Tunnel or nginx reverse proxy configured
- [ ] SSL/HTTPS working
- [ ] Steam OAuth redirects to correct URL
- [ ] Push notifications working (if configured)
- [ ] Discord bot posting messages (if configured)

## Troubleshooting

### Database issues
```bash
# Check database file
ls -lh data/inhouse.db

# Reset database (WARNING: deletes all data)
rm data/inhouse.db
# Server will create fresh DB on startup
```

### Permission issues
```bash
# Fix ownership
sudo chown -R USERNAME:USERNAME /path/to/destination/dota-inhouse

# Fix execute permissions
chmod +x bin/server
chmod +x set_env.sh
```

### Port already in use
```bash
# Find process using port 8080
sudo lsof -i :8080
# Kill if needed
sudo kill -9 PID
```

### Cloudflare Tunnel not connecting
```bash
# Check tunnel status
cloudflared tunnel info dota-inhouse

# Check logs
sudo journalctl -u cloudflared -f
```

## Rollback

To revert to old machine, simply:
1. Stop services on new machine
2. Point Cloudflare DNS back to old machine's IP
3. Restart services on old machine

## Notes for Claude Agent

When performing this migration:
1. Ask user for: NEW_MACHINE_IP, USERNAME, destination path
2. Ask user which Cloudflare option they prefer (Tunnel or traditional nginx)
3. Use `rsync` with `--dry-run` first to preview file transfer
4. After transfer, verify critical files exist: `go.mod`, `cmd/server/main.go`, `set_env.sh`
5. Build project before configuring services
6. Test server startup with `DEV_MODE=true` before setting up systemd service
7. Verify Cloudflare connectivity before updating DNS records


notes from user:
ip of old machine: 192.168.3.9
