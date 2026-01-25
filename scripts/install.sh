set -e

echo "Installing Strct Agent..."

# Create Directories
mkdir -p /mnt/data
mkdir -p /etc/strct

# Install dependencies
apt-get update
apt-get install -y docker.io network-manager

# Enable Docker
systemctl enable --now docker

# Copy Binary
cp cloud-agent /usr/local/bin/
chmod +x /usr/local/bin/cloud-agent

# Create Systemd Service
cat <<EOF > /etc/systemd/system/cloud-agent.service
[Unit]
Description=Strct Agent
After=network-online.target docker.service
Wants=network-online.target docker.service

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/cloud-agent
Restart=always
RestartSec=5s
Environment="VPS_IP=157.90.167.157"
Environment="VPS_PORT=7000"
Environment="AUTH_TOKEN=Struct33_Secret_Key_99"
Environment="DOMAIN=strct.org"

[Install]
WantedBy=multi-user.target
EOF

# Start
systemctl daemon-reload
systemctl enable cloud-agent
systemctl start cloud-agent

echo "Agent installed and running!"