#!/usr/bin/env bash
set -euo pipefail

# ── JoyCodeProxy 本地测试脚本 ────────────────────────────────────────
# 一键：构建前端 → 构建后端 → 卸载旧的 jcproxy → 安装新的 → 前台启动。
# 用于在本地快速验证改动。需要 sudo 权限来写入 /usr/local/bin。
#
# 用法:
#   ./dev.sh                # 默认端口 34891，HTTP（--tls=false），跳过凭据校验
#   PORT=8080 ./dev.sh      # 自定义端口
#   ./dev.sh -- --tls       # `--` 之后的参数原样透传给 `jcproxy serve`
#
# 停止：Ctrl+C（前台运行）。彻底移除：sudo jcproxy uninstall

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BIN_NAME="jcproxy"
TARGET="${INSTALL_DIR}/${BIN_NAME}"
PORT="${PORT:-34891}"
LOCAL_BIN="${ROOT_DIR}/${BIN_NAME}"

# `--` 之后的参数透传给 serve；默认本地测试参数便于免凭据、走 HTTP。
EXTRA_ARGS=("--tls=false" "--skip-validation" "--port" "$PORT")
if [[ "${1:-}" == "--" ]]; then
  shift
  EXTRA_ARGS=("$@")
fi

info() { echo "==> $*"; }

# 1. 构建前端（产物 embed 进二进制，必须先于 go build）
info "构建前端..."
(cd web && npm run build)

# 2. 构建后端二进制
info "构建后端二进制..."
go build -o "$LOCAL_BIN" ./cmd/JoyCodeProxy

# 3. 卸载电脑中已安装的 jcproxy（停服务 + 自删二进制，容错）
if command -v "$BIN_NAME" >/dev/null 2>&1; then
  info "卸载已安装的 jcproxy（停止服务并移除旧二进制）..."
  sudo "$BIN_NAME" uninstall || true
else
  info "未检测到已安装的 jcproxy，跳过卸载。"
fi

# 4. 安装新构建的二进制到 INSTALL_DIR
info "安装新二进制到 ${TARGET}（需要 sudo）..."
sudo mkdir -p "$INSTALL_DIR"
sudo cp "$LOCAL_BIN" "$TARGET"
sudo chmod +x "$TARGET"
# macOS：解除下载/构建隔离属性（容错）
if [[ "$(uname -s)" == "Darwin" ]]; then
  sudo xattr -d com.apple.quarantine "$TARGET" 2>/dev/null || true
fi

# 5. 校验安装并启动
info "已安装版本："
"$TARGET" version || true
echo ""
info "启动 jcproxy serve（前台，Ctrl+C 停止）..."
info "参数: ${EXTRA_ARGS[*]}"
echo ""
exec "$TARGET" serve "${EXTRA_ARGS[@]}"
