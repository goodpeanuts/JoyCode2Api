#!/usr/bin/env bash
# jcproxy 一键安装脚本
#
# 推荐用法（安装脚本随 release 发布，与产物版本绑定，不随分支漂移）：
#   curl -fsSL https://github.com/goodpeanuts/JoyCode2Api/releases/latest/download/install.sh | bash
#
# 也可从仓库获取：
#   curl -fsSL https://raw.githubusercontent.com/goodpeanuts/JoyCode2Api/main/install.sh | bash
#
# 自动探测平台、从 GitHub Releases 下载对应二进制（sha256 校验）、安装为 jcproxy 命令。
# 重跑本脚本 = 升级到最新（或 VERSION 指定）版本。
#
# 环境变量：
#   INSTALL_MODE   system（默认，安装到 /usr/local/bin，需要 sudo）
#                  user  （安装到 ~/.joycode-proxy/bin，免 sudo，自动提示配置 PATH）
#   INSTALL_DIR    自定义安装目录（设置后优先于 INSTALL_MODE）
#   VERSION        版本 tag，默认 latest
#   AUTO_START     1 = 安装后自动注册系统服务并启动（交互终端默认会询问；管道模式默认跳过）
#   SKIP_CHECKSUM  1 = 跳过 sha256 校验（不建议）
set -euo pipefail

REPO="goodpeanuts/JoyCode2Api"
BIN_NAME="jcproxy"
ASSET_PREFIX="joycode-proxy"   # 必须与 release.yml 的 BINARY_NAME、update.go 的 assetName() 一致
VERSION="${VERSION:-latest}"
INSTALL_MODE="${INSTALL_MODE:-system}"
SKIP_CHECKSUM="${SKIP_CHECKSUM:-0}"

err()  { echo "错误：${1}" >&2; exit 1; }
warn() { echo "警告：${1}" >&2; }
info() { echo "==> ${1}"; }

# ── 1. 探测平台 ──────────────────────────────────────────────
os=""
case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux)  os="linux" ;;
  MINGW*|MSYS*|CYGWIN*)
    err "检测到 Windows。请到 https://github.com/${REPO}/releases 手动下载 ${ASSET_PREFIX}-windows-amd64.exe 后运行 serve。" ;;
  *) err "不支持的操作系统：$(uname -s)。请到 https://github.com/${REPO}/releases 手动下载。" ;;
esac

arch=""
case "$(uname -m)" in
  arm64|aarch64) arch="arm64" ;;
  x86_64|amd64)  arch="amd64" ;;
  *) err "不支持的 CPU 架构：$(uname -m)。请到 https://github.com/${REPO}/releases 手动下载或用源码构建。" ;;
esac

asset="${ASSET_PREFIX}-${os}-${arch}"

# ── 2. 组装下载地址 ──────────────────────────────────────────
if [ "$VERSION" = "latest" ]; then
  base_url="https://github.com/${REPO}/releases/latest/download"
else
  base_url="https://github.com/${REPO}/releases/download/${VERSION}"
fi

# ── 3. 下载并校验 ────────────────────────────────────────────
command -v curl >/dev/null 2>&1 || err "需要 curl，请先安装。"
tmp="$(mktemp)"
cleanup() { rm -f "$tmp" "$tmp.sha" "$tmp.sum"; }
trap cleanup EXIT

info "下载 ${asset}（${VERSION}）..."
curl -fL --progress-bar "${base_url}/${asset}" -o "$tmp" || err "下载失败：${base_url}/${asset}"
chmod +x "$tmp"

if [ "$SKIP_CHECKSUM" = "1" ]; then
  warn "已按 SKIP_CHECKSUM=1 跳过 sha256 校验。"
else
  checksums="$(mktemp)"
  if curl -fsL "${base_url}/checksums-sha256.txt" -o "$checksums"; then
    expected="$(grep " ${asset}\$" "$checksums" | awk '{print $1}' || true)"
    if [ -n "$expected" ]; then
      if command -v shasum >/dev/null 2>&1; then
        actual="$(shasum -a 256 "$tmp" | awk '{print $1}')"
      elif command -v sha256sum >/dev/null 2>&1; then
        actual="$(sha256sum "$tmp" | awk '{print $1}')"
      else
        warn "系统缺少 shasum/sha256sum，跳过校验。"
        actual="$expected"
      fi
      if [ "$actual" != "$expected" ]; then
        err "sha256 校验失败！期望 ${expected}，实际 ${actual}。下载可能不完整或被篡改，已中止。"
      fi
      info "sha256 校验通过。"
    else
      warn "checksums 文件中未找到 ${asset}（可能是旧版 release），跳过校验。"
    fi
    rm -f "$checksums"
  else
    warn "未能下载 checksums-sha256.txt（可能是旧版 release），跳过校验。"
  fi
fi

# ── 4. 确定安装目录 ──────────────────────────────────────────
if [ -n "${INSTALL_DIR:-}" ]; then
  : # 显式 INSTALL_DIR 优先
elif [ "$INSTALL_MODE" = "user" ]; then
  INSTALL_DIR="${HOME}/.joycode-proxy/bin"
elif [ "$INSTALL_MODE" = "system" ]; then
  INSTALL_DIR="/usr/local/bin"
else
  err "INSTALL_MODE 仅支持 system / user，当前值：${INSTALL_MODE}"
fi
target="${INSTALL_DIR}/${BIN_NAME}"

# ── 5. 安装（幂等，重跑即升级）───────────────────────────────
info "安装到 ${target}"
used_sudo=""
if [ -w "$INSTALL_DIR" ] || { [ ! -e "$INSTALL_DIR" ] && mkdir -p "$INSTALL_DIR" 2>/dev/null; }; then
  mv -f "$tmp" "$target"
else
  info "目标目录需要管理员权限，使用 sudo..."
  command -v sudo >/dev/null 2>&1 \
    || err "目标目录 ${INSTALL_DIR} 不可写且系统没有 sudo。可改用：INSTALL_MODE=user（安装到 ~/.joycode-proxy/bin）。"
  used_sudo="1"
  sudo mkdir -p "$INSTALL_DIR"
  sudo mv -f "$tmp" "$target"
  sudo chmod 755 "$target"
fi
trap - EXIT
cleanup 2>/dev/null || true

# ── 6. macOS 清除隔离属性（容错；用与安装一致的权限）─────────
if [ "$os" = "darwin" ]; then
  if [ -n "$used_sudo" ]; then
    sudo xattr -d com.apple.quarantine "$target" 2>/dev/null || true
    sudo xattr -d com.apple.provenance "$target" 2>/dev/null || true
  else
    xattr -d com.apple.quarantine "$target" 2>/dev/null || true
    xattr -d com.apple.provenance "$target" 2>/dev/null || true
  fi
fi

# ── 7. 运行中实例提示 ────────────────────────────────────────
if command -v pgrep >/dev/null 2>&1 && pgrep -f "${BIN_NAME}.* serve" >/dev/null 2>&1; then
  warn "检测到 jcproxy 正在运行，二进制已被替换但旧进程仍占用内存中的旧版本。"
  echo "    如需立即生效，请重启：jcproxy daemon restart （或重新 service install / 手动重启 serve）"
fi

# ── 8. 用户模式：PATH 配置 ───────────────────────────────────
path_hint_needed=0
if [ "$INSTALL_MODE" = "user" ]; then
  case ":${PATH}:" in
    *":${INSTALL_DIR}:"*) : ;;
    *)
      path_hint_needed=1
      rc_file=""
      case "${SHELL:-}" in
        */zsh)  rc_file="${HOME}/.zshrc" ;;
        */bash) rc_file="${HOME}/.bashrc" ;;
      esac
      export_line="export PATH=\"${INSTALL_DIR}:\$PATH\""
      if [ -t 0 ] && [ -n "$rc_file" ]; then
        printf "%s\n%s" "  ${BIN_NAME} 所在目录不在 PATH 中。" "  写入 ${export_line} 到 ${rc_file}？[y/N] "
        read -r answer </dev/tty
        if [ "${answer:-}" = "y" ] || [ "${answer:-}" = "Y" ]; then
          printf '\n# jcproxy (JoyCode2Api)\n%s\n' "$export_line" >> "$rc_file"
          info "已写入 ${rc_file}，请执行 source ${rc_file} 或重开终端。"
          path_hint_needed=0
        fi
      fi
      if [ "$path_hint_needed" = "1" ]; then
        echo ""
        warn "目录 ${INSTALL_DIR} 不在 PATH 中，请手动执行：${export_line}"
      fi
      ;;
  esac
fi

# ── 9. 可选：注册系统服务 ────────────────────────────────────
do_service_install() {
  info "注册系统服务（开机自启）..."
  "$target" service install
}
if [ "${AUTO_START:-0}" = "1" ]; then
  do_service_install
elif [ -t 0 ]; then
  printf "%s" "  是否注册系统服务（launchd/systemd，开机自启）？[y/N] "
  read -r answer </dev/tty
  if [ "${answer:-}" = "y" ] || [ "${answer:-}" = "Y" ]; then
    do_service_install
  fi
fi

# ── 10. 完成 ─────────────────────────────────────────────────
echo ""
info "安装完成！"
"$target" version 2>/dev/null | head -1 || true
echo ""
echo "  启动代理：      ${BIN_NAME} serve"
echo "  后台常驻：      ${BIN_NAME} daemon start"
echo "  注册系统服务：  ${BIN_NAME} service install"
echo "  自升级：        ${BIN_NAME} update"
echo "  卸载：          ${BIN_NAME} uninstall"
echo ""
if ! command -v "$BIN_NAME" >/dev/null 2>&1; then
  echo "提示：${INSTALL_DIR} 似乎不在当前 PATH 中，新开终端或 source 对应 rc 文件后即可直接使用 ${BIN_NAME}。"
fi
