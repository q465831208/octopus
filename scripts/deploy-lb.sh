#!/bin/bash
# ============================================================
# Octopus 请求级负载均衡版 VPS 部署脚本（在 217.142.187.171 上执行）
# 镜像由 GitHub Actions 在 fork 仓库构建并推送到 GHCR
#   ghcr.io/<你的github>/octopus:lb   (linux/amd64 + linux/arm64)
# 数据卷 /opt/octopus/data 保持不变, 端口仍为 7070
#
# 用法: bash deploy-lb.sh
# 可选: IMAGE=ghcr.io/xxx/octopus:lb PORT=7071 bash deploy-lb.sh
# ============================================================
set -euo pipefail

IMAGE="${IMAGE:-ghcr.io/q465831208/octopus:lb}"
DATA_VOL="${DATA_VOL:-/opt/octopus/data}"
PORT="${PORT:-7070}"
CONTAINER="octopus"

echo "==> 1/4 拉取镜像 $IMAGE"
docker pull "$IMAGE"

echo "==> 2/4 备份旧容器（数据卷不动）"
if docker ps -a --format '{{.Names}}' | grep -qx "$CONTAINER"; then
    docker rename "$CONTAINER" "${CONTAINER}-old-$(date +%s)"
    echo "    旧容器已改名 ${CONTAINER}-old-*（可回滚）"
fi

echo "==> 3/4 启动新容器 (端口 $PORT)"
docker run -d --name "$CONTAINER" --restart unless-stopped \
    -v "$DATA_VOL":/app/data \
    -p "$PORT":8080 \
    "$IMAGE"

echo "==> 4/4 验证"
sleep 5
docker ps -f name="$CONTAINER" --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}'
curl -sI "http://127.0.0.1:$PORT/" | head -3 || true
echo ""
echo "部署完成: http://217.142.187.171:$PORT  (admin / 39497981)"
echo ""
echo "回滚方法（如需）:"
echo "  docker stop $CONTAINER && docker rm $CONTAINER"
echo "  docker rename \$(docker ps -a --format '{{.Names}}' | grep '${CONTAINER}-old-' | head -1) $CONTAINER"
echo "  docker start $CONTAINER"
