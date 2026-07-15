#!/usr/bin/env bash
# jcproxy 一键安装脚本
#
#   curl -fsSL https://raw.githubusercontent.com/vibe-coding-labs/JoyCodeProxy/main/install.sh | bash
#
# 自动探测平台、从 GitHub Releases 下载最新二进制，安装为全局命令 jcproxy。
# 可用环境变量：
#   INSTALL_DIR   安装目录（默认 /usr/local/bin）
#   VERSION       指定版本 tag（默认 latest）
set -euo pipefail

REPO="vibe-coding-labs/JoyCodeProxy"
BIN_NAME="jcproxy"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${VERSION:-latest}"

err() { echo "错误：$*" >&2; exit 1; }
info() { echo "==> $*"; }

# 1. 探测平台
os=""
case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux)  os="linux" ;;
  *)      err "不支持的操作系统：$(uname -s)。目前仅支持 macOS 和 Linux，请到 https://github.com/${REPO}/releases 手动下载。" ;;
esac

arch=""
case "$(uname -m)" in
  arm64|aarch64) arch="arm64" ;;
  x86_64|amd64)  arch="amd64" ;;
  *)             arch="$(uname -m)" ;;
esac

# 目前 Releases 只产出 darwin-arm64 和 linux-amd64
asset="joycode-proxy-${os}-${arch}"
case "${os}-${arch}" in
  darwin-arm64|linux-amd64) ;;
  *) err "暂无 ${os}-${arch} 的预编译产物。请到 https://github.com/${REPO}/releases 手动下载，或用源码构建。" ;;
esac

# 2. 组装下载地址
if [ "$VERSION" = "latest" ]; then
  url="https://github.com/${REPO}/releases/latest/download/${asset}"
else
  url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
fi

# 3. 下载到临时文件
command -v curl >/dev/null 2>&1 || err "需要 curl，请先安装。"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
info "下载 ${asset}（${VERSION}）..."
curl -fL --progress-bar "$url" -o "$tmp" || err "下载失败：$url"

chmod +x "$tmp"

# 4. 安装到 INSTALL_DIR，重命名为 jcproxy
target="${INSTALL_DIR}/${BIN_NAME}"
info "安装到 ${target}"
used_sudo=""
if [ -w "$INSTALL_DIR" ] || { [ ! -e "$INSTALL_DIR" ] && mkdir -p "$INSTALL_DIR" 2>/dev/null; }; then
  mv "$tmp" "$target"
else
  info "目标目录需要管理员权限，使用 sudo..."
  command -v sudo >/dev/null 2>&1 || err "目标目录 ${INSTALL_DIR} 不可写，且系统没有 sudo。请用 INSTALL_DIR=~/.local/bin 改到可写目录。"
  used_sudo="1"
  sudo mkdir -p "$INSTALL_DIR"
  sudo mv "$tmp" "$target"
fi
trap - EXIT

# 5. macOS 解除下载隔离属性（容错）。用与安装同样的权限，避免 root 拥有的文件被普通用户 xattr 拒绝。
if [ "$os" = "darwin" ]; then
  if [ -n "$used_sudo" ]; then
    sudo xattr -d com.apple.quarantine "$target" 2>/dev/null || true
  else
    xattr -d com.apple.quarantine "$target" 2>/dev/null || true
  fi
fi

# 6. 完成提示
echo ""
info "安装完成！"
echo ""
echo "  启动代理：      ${BIN_NAME} serve"
echo "  后台常驻：      ${BIN_NAME} daemon start"
echo "  注册系统服务：  ${BIN_NAME} service install"
echo "  卸载：          ${BIN_NAME} uninstall"
echo ""
if ! command -v "$BIN_NAME" >/dev/null 2>&1; then
  echo "提示：${INSTALL_DIR} 似乎不在 PATH 中，请将其加入 PATH，或直接用 ${target} 运行。"
fi
