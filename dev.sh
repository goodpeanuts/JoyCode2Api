#!/usr/bin/env bash
set -euo pipefail

# ── JoyCode2Api 本地开发脚本 ────────────────────────────────────────
# 构建（前端+后端）→ 仓库内运行。默认前台启动、不触碰已安装的 jcproxy。
#
# 用法:
#   ./dev.sh                # 默认端口 34891，HTTP（--tls=false），跳过凭据校验
#   PORT=8080 ./dev.sh      # 自定义端口
#   ./dev.sh -- --tls       # `--` 之后的参数原样透传给 serve
#   ./dev.sh --install      # 额外把新二进制安装到 INSTALL_DIR（默认 /usr/local/bin，
#                           # 需要 sudo；不会卸载/停止已安装的服务）
#
# 已安装的正式 jcproxy 不受影响：如需让本机构建版本接管端口，
# 先 jcproxy daemon stop / service uninstall，再运行本脚本。

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BIN_NAME="jcproxy"
PORT="${PORT:-34891}"
LOCAL_BIN="${ROOT_DIR}/${BIN_NAME}"

# `--` 之后的参数透传给 serve；默认本地测试参数便于免凭据、走 HTTP。
EXTRA_ARGS=("--tls=false" "--skip-validation" "--port" "$PORT")
if [[ "${1:-}" == "--" ]]; then
  shift
  EXTRA_ARGS=("$@")
fi

DO_INSTALL=0
if [[ "${1:-}" == "--install" ]]; then
  DO_INSTALL=1
  shift || true
fi

info() { echo "==> ${1}"; }

# 1. 构建前端（产物 embed 进二进制，必须先于 go build）
info "构建前端..."
(cd web && npm run build)

# 2. 构建后端二进制到仓库目录
info "构建后端二进制 ${LOCAL_BIN} ..."
go build -o "$LOCAL_BIN" ./cmd/JoyCodeProxy

# 3. 可选：安装到系统目录（幂等覆盖，不动已安装的服务/守护进程）
if [[ "$DO_INSTALL" == "1" ]]; then
  target="${INSTALL_DIR}/${BIN_NAME}"
  info "安装新二进制到 ${target} ..."
  if [[ -w "$INSTALL_DIR" ]]; then
    cp "$LOCAL_BIN" "$target"
  else
    sudo mkdir -p "$INSTALL_DIR"
    sudo cp "$LOCAL_BIN" "$target"
    sudo chmod 755 "$target"
  fi
  if [[ "$(uname -s)" == "Darwin" ]]; then
    if [[ -w "$INSTALL_DIR" ]]; then
      xattr -d com.apple.quarantine "$target" 2>/dev/null || true
      xattr -d com.apple.provenance "$target" 2>/dev/null || true
    else
      sudo xattr -d com.apple.quarantine "$target" 2>/dev/null || true
      sudo xattr -d com.apple.provenance "$target" 2>/dev/null || true
    fi
  fi
  info "已安装版本："
  "$target" version || true
  echo ""
fi

# 4. 前台启动（Ctrl+C 停止）
info "启动 ${LOCAL_BIN} serve（前台，Ctrl+C 停止）..."
info "参数: ${EXTRA_ARGS[*]}"
echo ""
exec "$LOCAL_BIN" serve "${EXTRA_ARGS[@]}"
