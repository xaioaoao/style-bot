#!/bin/bash
set -e

echo "=== Style Bot Deploy ==="

# 1. 安装 Docker
if ! command -v docker &>/dev/null; then
    echo "Installing Docker..."
    curl -fsSL https://get.docker.com | sh
    sudo systemctl enable docker && sudo systemctl start docker
fi
echo "[OK] Docker ready"

# 2. 安装 Go
if ! command -v go &>/dev/null; then
    echo "Installing Go..."
    wget -q https://go.dev/dl/go1.23.6.linux-amd64.tar.gz
    sudo tar -C /usr/local -xzf go1.23.6.linux-amd64.tar.gz
    rm go1.23.6.linux-amd64.tar.gz
    export PATH=$PATH:/usr/local/go/bin
    echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
fi
echo "[OK] Go $(go version | awk '{print $3}')"

# 3. 编译
echo "Building bot..."
cd ~/style-bot
go build -o bot ./cmd/bot
echo "[OK] Bot built"

# 4. 创建目录
mkdir -p data/vectors data/sessions data/persona napcat-config ntqq

# 5. 检查数据文件
if [ ! -f data/persona.json ]; then
    echo ""
    echo "[!] data/persona.json 不存在"
    echo "    请上传 persona.json 到 ~/style-bot/data/"
    echo ""
fi

if [ ! -d data/vectors ] || [ -z "$(ls -A data/vectors 2>/dev/null)" ]; then
    echo "[!] data/vectors/ 为空"
    echo "    请上传 vectors 目录到 ~/style-bot/data/"
    echo ""
fi

# 6. 启动 NapCatQQ
if sudo docker ps -a --format '{{.Names}}' | grep -q napcat; then
    echo "NapCatQQ already exists, restarting..."
    sudo docker restart napcat
else
    echo "Starting NapCatQQ..."
    sudo docker run -d \
        --name napcat \
        -p 3001:3001 \
        -p 6099:6099 \
        -v ~/style-bot/napcat-config:/app/napcat/config \
        -v ~/style-bot/ntqq:/app/.config/QQ \
        --mac-address "02:42:ac:11:00:02" \
        --restart always \
        mlikiowa/napcat-docker:latest
fi
echo "[OK] NapCatQQ running"

# 7. 获取服务器 IP
SERVER_IP=$(curl -s ifconfig.me 2>/dev/null || echo "YOUR_SERVER_IP")

echo ""
echo "========================================"
echo " 部署完成！接下来："
echo "========================================"
echo ""
echo " 1. 扫码登录 QQ 小号："
echo "    http://${SERVER_IP}:6099/webui"
echo ""
echo " 2. 上传数据文件（如果还没上传）："
echo "    - data/persona.json"
echo "    - data/vectors/ 目录"
echo ""
echo " 3. 修改配置："
echo "    vi ~/style-bot/configs/config.yaml"
echo "    填入 target_qq, owner_qq, my_name, target_name"
echo ""
echo " 4. 启动 Bot："
echo "    GEMINI_API_KEY=你的key ./bot -config configs/config.yaml"
echo ""
echo " 5. 后台运行："
echo "    nohup env GEMINI_API_KEY=你的key ./bot -config configs/config.yaml > bot.log 2>&1 &"
echo ""
echo "========================================"
