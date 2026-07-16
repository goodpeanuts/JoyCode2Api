package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

const defaultReleaseRepo = "goodpeanuts/JoyCode2Api"

var (
	updateForce bool
	updateRepo  string
)

var updateCmd = &cobra.Command{
	Use:     "update",
	Short:   "更新到最新版本",
	GroupID: "service",
	Long: `检测 GitHub 上的最新 release，若比当前版本新则下载、替换当前二进制，
并重启已安装的后台服务/守护进程。

默认从 ` + defaultReleaseRepo + ` 拉取。开发构建（dev-*）需加 --force。`,
	Example: `  # 更新到最新版
  jcproxy update

  # 强制重装最新版（开发构建或想重装时用）
  jcproxy update --force`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUpdate()
	},
}

func init() {
	updateCmd.Flags().BoolVar(&updateForce, "force", false, "跳过版本比较，强制重新安装最新版")
	updateCmd.Flags().StringVar(&updateRepo, "repo", defaultReleaseRepo, "GitHub 仓库 (owner/name)")
	rootCmd.AddCommand(updateCmd)
}

// assetName returns the release asset filename for the given platform, and
// whether the platform has a published prebuilt binary.
func assetName(goos, goarch string) (string, bool) {
	switch goos + "-" + goarch {
	case "darwin-arm64", "linux-amd64":
		return fmt.Sprintf("joycode-proxy-%s-%s", goos, goarch), true
	default:
		return "", false
	}
}

// githubAPIBase is the GitHub API root; overridable in tests.
var githubAPIBase = "https://api.github.com"

// latestTag queries the GitHub API for the repo's latest release tag.
func latestTag(repo string) (string, error) {
	url := githubAPIBase + "/repos/" + repo + "/releases/latest"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "jcproxy-update")
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("查询最新版本失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("查询最新版本失败: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", fmt.Errorf("解析 release 信息失败: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("release 未包含 tag_name")
	}
	return rel.TagName, nil
}

// needsUpdate reports whether latest is strictly newer than current (semver).
// A dev/invalid current version returns needsForce=true so the caller can
// require --force rather than silently comparing an unparseable version.
func needsUpdate(current, latest string) (newer bool, needsForce bool) {
	cur := ensureV(current)
	lat := ensureV(latest)
	if !semver.IsValid(cur) {
		// dev build or unparseable → can't compare; require --force.
		return false, true
	}
	if !semver.IsValid(lat) {
		return false, false
	}
	return semver.Compare(lat, cur) > 0, false
}

func ensureV(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return v
	}
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}

// download fetches url into a temp file, marks it executable, and returns its path.
func download(url string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "jcproxy-update")

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败: HTTP %d (%s)", resp.StatusCode, url)
	}

	tmp, err := os.CreateTemp("", "jcproxy-update-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	written, err := io.Copy(tmp, resp.Body)
	if err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("写入下载文件失败: %w", err)
	}
	tmp.Close()
	// Guard against a silently truncated body (clean connection close mid-transfer).
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		os.Remove(tmpPath)
		return "", fmt.Errorf("下载不完整: 期望 %d 字节，实际 %d 字节", resp.ContentLength, written)
	}
	if written == 0 {
		os.Remove(tmpPath)
		return "", fmt.Errorf("下载内容为空")
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	return tmpPath, nil
}

// dirWritable reports whether the directory allows creating a file (i.e. we can
// replace a binary inside it without elevation).
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".jcproxy-wtest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// replaceBinary moves src over dst, using sudo when dst's directory isn't
// writable by the current user. On macOS the download-quarantine attribute is
// cleared afterwards (best-effort).
func replaceBinary(src, dst string) error {
	dir := filepath.Dir(dst)
	if dirWritable(dir) {
		// Same-dir rename is atomic; replacing a running binary's inode is safe on POSIX.
		staged := filepath.Join(dir, ".jcproxy-new")
		if err := os.Rename(src, staged); err != nil {
			// src may be on a different filesystem (temp dir) — fall back to copy.
			if cerr := copyFile(src, staged); cerr != nil {
				return fmt.Errorf("暂存新二进制失败: %w", cerr)
			}
			os.Remove(src)
		}
		if err := os.Chmod(staged, 0o755); err != nil {
			os.Remove(staged)
			return err
		}
		if err := os.Rename(staged, dst); err != nil {
			os.Remove(staged)
			return fmt.Errorf("替换二进制失败: %w", err)
		}
		clearQuarantine(dst, false)
		return nil
	}

	// Not writable — elevate just for the file placement.
	fmt.Println("  目标目录需要管理员权限，使用 sudo...")
	if _, err := exec.LookPath("sudo"); err != nil {
		return fmt.Errorf("目标目录 %s 不可写，且系统没有 sudo，请手动替换 %s", dir, dst)
	}
	cmd := exec.Command("sudo", "install", "-m", "0755", src, dst)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo 替换二进制失败: %w", err)
	}
	os.Remove(src)
	clearQuarantine(dst, true)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// clearQuarantine removes the macOS download-quarantine xattr (no-op elsewhere).
func clearQuarantine(path string, useSudo bool) {
	if runtime.GOOS != "darwin" {
		return
	}
	if useSudo {
		exec.Command("sudo", "xattr", "-d", "com.apple.quarantine", path).Run()
	} else {
		exec.Command("xattr", "-d", "com.apple.quarantine", path).Run()
	}
}

// serviceUnitPath returns the launchd plist (darwin) or systemd user unit
// (linux) path for the installed service, or "" on other platforms.
func serviceUnitPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", plistName)
	case "linux":
		return filepath.Join(home, ".config", "systemd", "user", serviceLabel+".service")
	default:
		return ""
	}
}

// installedServicePort reports whether a service unit exists and, if so, the
// port it was configured with (falling back to servePort's default if the unit
// exists but no port can be parsed).
func installedServicePort() (port int, installed bool) {
	path := serviceUnitPath()
	if path == "" {
		return 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	// Both the plist (<string>34891</string> after a --port arg) and the systemd
	// unit (ExecStart=... serve --port 34891) contain "--port <N>"; parse that.
	if p, ok := parsePortAfterFlag(string(data)); ok {
		return p, true
	}
	return servePort, true
}

// parsePortAfterFlag extracts the integer following the first "--port" token in
// the given text (whitespace- or tag-separated), used to recover the configured
// service port from a plist/systemd unit.
func parsePortAfterFlag(s string) (int, bool) {
	idx := strings.Index(s, "--port")
	if idx < 0 {
		return 0, false
	}
	rest := s[idx+len("--port"):]
	// Skip non-digits (whitespace, XML tags like "</string><string>").
	i := 0
	for i < len(rest) && (rest[i] < '0' || rest[i] > '9') {
		i++
	}
	j := i
	for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
		j++
	}
	if i == j {
		return 0, false
	}
	p, err := strconv.Atoi(rest[i:j])
	if err != nil || p <= 0 {
		return 0, false
	}
	return p, true
}

func runUpdate() error {
	// 1. Platform.
	asset, ok := assetName(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return fmt.Errorf("暂无 %s-%s 的预编译产物，请到 https://github.com/%s/releases 手动下载或用源码构建",
			runtime.GOOS, runtime.GOARCH, updateRepo)
	}

	// 2. Latest version.
	fmt.Printf("正在检查最新版本（%s）...\n", updateRepo)
	tag, err := latestTag(updateRepo)
	if err != nil {
		return err
	}
	fmt.Printf("  当前版本: %s\n", Version)
	fmt.Printf("  最新版本: %s\n", tag)

	// 3. Compare.
	if !updateForce {
		newer, needsForce := needsUpdate(Version, tag)
		if needsForce {
			return fmt.Errorf("当前为开发构建（%s），无法与 release 比较版本。如需更新请加 --force", Version)
		}
		if !newer {
			fmt.Println("已是最新版本，无需更新。")
			return nil
		}
	}

	// 4. Download.
	dlURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", updateRepo, tag, asset)
	fmt.Printf("→ 下载 %s ...\n", asset)
	tmpPath, err := download(dlURL)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath)

	// 5. Locate the installed binary.
	target, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法定位当前二进制: %w", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(target); rerr == nil {
		target = resolved
	}

	// Capture prior state so we only restart what existed before, and preserve
	// the configured service port instead of resetting it to the default.
	_, daemonWasRunning := checkRunningDaemon()
	svcPort, svcInstalled := installedServicePort()

	// 6. Replace the binary FIRST. Replacing a running executable's inode via an
	// in-place rename is safe on POSIX (the old process keeps its open file), and
	// doing it before touching the service means a failed/cancelled replace never
	// leaves the user with a stopped service.
	fmt.Printf("→ 替换二进制: %s\n", target)
	if err := replaceBinary(tmpPath, target); err != nil {
		return fmt.Errorf("%w\n  二进制未改动，服务未受影响。请重试或手动安装", err)
	}

	// 7. Restart the service/daemon only if one was managing the proxy before.
	if svcInstalled {
		fmt.Println("→ 重启系统服务")
		if err := stopDaemon(); err != nil {
			fmt.Printf("  警告：停止守护进程时出错：%v\n", err)
		}
		if err := uninstallService(); err != nil {
			fmt.Printf("  警告：移除旧系统服务时出错：%v\n", err)
		}
		if err := installService(svcPort); err != nil {
			fmt.Printf("  警告：重装系统服务失败：%v\n", err)
			fmt.Printf("  请手动执行 `jcproxy service install -p %d` 启动服务。\n", svcPort)
		}
	} else if daemonWasRunning {
		fmt.Println("→ 重启守护进程")
		if err := stopDaemon(); err != nil {
			fmt.Printf("  警告：停止守护进程时出错：%v\n", err)
		}
		if err := startDaemon(); err != nil {
			fmt.Printf("  警告：重启守护进程失败：%v\n", err)
			fmt.Println("  请手动执行 `jcproxy daemon start` 启动。")
		}
	} else {
		fmt.Println("  未检测到已安装的服务或守护进程，仅替换了二进制。")
		fmt.Println("  可用 `jcproxy serve` 前台启动，或 `jcproxy service install` 注册后台服务。")
	}

	// 8. Done.
	fmt.Printf("更新完成：%s → %s\n", Version, tag)
	return nil
}
