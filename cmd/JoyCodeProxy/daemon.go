package main

import (
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/logrot"
)

const (
	daemonChildEnv      = "_JOYCODE_DAEMON_CHILD"
	daemonSupervisorEnv = "_JOYCODE_DAEMON_SUPERVISOR"
	daemonPortEnv       = "_JOYCODE_DAEMON_PORT"
	daemonVerboseEnv    = "_JOYCODE_DAEMON_VERBOSE"
	daemonSkipValEnv    = "_JOYCODE_DAEMON_SKIP_VALIDATION"
	daemonHostEnv       = "_JOYCODE_DAEMON_HOST"
	daemonTLSEnv        = "_JOYCODE_DAEMON_TLS"
	pidFileName         = ".joycode-proxy/daemon.pid"
	logFileName         = ".joycode-proxy/logs/daemon.log"
	maxRestartDelay     = 30 * time.Second
	baseRestartDelay    = 1 * time.Second
	// A child that exits sooner than this is considered a "fast failure"
	// (likely a deterministic config error rather than a transient crash).
	fastFailThreshold = 5 * time.Second
	// Stop restarting after this many consecutive fast failures.
	maxFastFailures = 5
)

var (
	daemonPIDFile string
	daemonLogFile string
)

var daemonCmd = &cobra.Command{
	Use:     "daemon",
	Short:   "以守护进程模式运行（崩溃自动重启）",
	Long:    "以后台守护进程模式启动代理服务。自动在后台运行，崩溃后自动重启（指数退避），日志写入文件。",
	GroupID: "service",
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "启动守护进程",
	Long: "启动 JoyCode Proxy 守护进程。Supervisor 进程监控子进程，" +
		"子进程崩溃时自动重启（1s → 2s → 4s → ... → 30s 指数退避）。",
	Example: `  # 使用默认端口启动
  jcproxy daemon start

  # 指定端口
  jcproxy daemon start -p 8080`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return startDaemon()
	},
}

var daemonStopCmd = &cobra.Command{
	Use:     "stop",
	Short:   "停止守护进程",
	Example: `  jcproxy daemon stop`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return stopDaemon()
	},
}

var daemonRestartCmd = &cobra.Command{
	Use:     "restart",
	Short:   "重启守护进程",
	Example: `  jcproxy daemon restart`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// stopDaemon now waits for the old process to fully exit (with a
		// SIGKILL fallback), so restart no longer needs a fixed sleep that
		// could race with the old instance releasing the port.
		if err := stopDaemon(); err != nil {
			log.Printf("stop warning: %v", err)
		}
		return startDaemon()
	},
}

var daemonStatusCmd = &cobra.Command{
	Use:     "status",
	Short:   "查看守护进程状态",
	Example: `  jcproxy daemon status`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return daemonStatusCmdRun()
	},
}

var daemonLogsCmd = &cobra.Command{
	Use:     "logs",
	Short:   "查看守护进程日志（最后 N 行）",
	Example: `  jcproxy daemon logs
  jcproxy daemon logs -n 50`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return tailDaemonLogs(daemonLines)
	},
}

var daemonLines int
var daemonHost string
var daemonTLS bool

func init() {
	home, _ := os.UserHomeDir()
	daemonPIDFile = filepath.Join(home, pidFileName)
	daemonLogFile = filepath.Join(home, logFileName)

	daemonLogsCmd.Flags().IntVarP(&daemonLines, "lines", "n", 20, "显示最后 N 行日志")
	daemonStartCmd.Flags().StringVar(&daemonHost, "host", "", "透传给 serve 的绑定地址（默认 0.0.0.0）")
	daemonStartCmd.Flags().BoolVar(&daemonTLS, "tls", true, "透传给 serve 的 TLS 开关")

	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonRestartCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
	daemonCmd.AddCommand(daemonLogsCmd)
	// --port/-p 为根级持久旗标（root.go init 注册），此处不再重复定义。
	rootCmd.AddCommand(daemonCmd)
}

// startDaemon forks a supervisor process that monitors the server child.
func startDaemon() error {
	if os.Getenv(daemonChildEnv) != "" || os.Getenv(daemonSupervisorEnv) == "1" {
		return fmt.Errorf("already running as daemon (nested start not allowed)")
	}

	if pid, running := checkRunningDaemon(); running {
		return fmt.Errorf("daemon already running (PID %d). Use 'daemon restart' or 'daemon stop' first", pid)
	}

	// Best-effort port probe: fail fast with a clear message instead of
	// spawning a supervisor whose child immediately dies on bind.
	if servePort > 0 {
		if ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(servePort))); err != nil {
			return fmt.Errorf("端口 %d 已被占用（可能已有 serve/服务实例在运行）。请先停止旧实例，或用 --port 换端口", servePort)
		} else {
			ln.Close()
		}
	}

	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine binary path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(daemonLogFile), 0755); err != nil {
		return fmt.Errorf("cannot create log directory: %w", err)
	}

	env := append(os.Environ(),
		daemonSupervisorEnv+"=1",
		daemonPortEnv+"="+strconv.Itoa(servePort),
	)
	if verbose {
		env = append(env, daemonVerboseEnv+"=1")
	}
	if skipValidation {
		env = append(env, daemonSkipValEnv+"=1")
	}
	if daemonHost != "" {
		env = append(env, daemonHostEnv+"="+daemonHost)
	}
	if !daemonTLS {
		env = append(env, daemonTLSEnv+"=0")
	}

	cmd := exec.Command(binPath)
	cmd.Env = env
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	setProcAttrDetached(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon supervisor: %w", err)
	}

	cmd.Process.Release()

	// Wait briefly for PID file to be written
	time.Sleep(200 * time.Millisecond)
	pidData, err := readPIDFile()
	if err != nil {
		fmt.Printf("Daemon supervisor started (PID %d, port %d)\n", cmd.Process.Pid, servePort)
	} else {
		fmt.Printf("Daemon started (PID %d, port %d)\n", pidData.PID, pidData.Port)
	}
	fmt.Printf("  Logs: %s\n", daemonLogFile)
	fmt.Printf("  PID:  %s\n", daemonPIDFile)
	fmt.Printf("  URL:  http://127.0.0.1:%d/ (Dashboard) / http://127.0.0.1:%d/v1 (API)\n", servePort, servePort)
	return nil
}

func stopDaemon() error {
	pidData, err := readPIDFile()
	if err != nil {
		fmt.Println("Daemon not running (PID file not found).")
		return nil
	}

	proc, err := os.FindProcess(pidData.PID)
	if err != nil || !isProcessAlive(proc) {
		removePIDFile()
		fmt.Println("Daemon not running (stale PID file removed).")
		return nil
	}

	// Guard against PID reuse: verify the PID still belongs to a jcproxy
	// process before signalling it, so we never SIGTERM an unrelated process.
	if !daemonProcessMatches(pidData) {
		removePIDFile()
		fmt.Printf("PID %d no longer belongs to jcproxy (PID was reused); stale PID file removed.\n", pidData.PID)
		return nil
	}

	if err := terminateProcess(proc); err != nil {
		removePIDFile()
		fmt.Printf("Daemon process %d not responding: %v\n", pidData.PID, err)
		return nil
	}

	// Poll for exit (Wait() is useless here: this CLI is not the parent, so
	// it would return ECHILD immediately). Escalate to SIGKILL after 5s.
	if waitForProcessExit(proc, 5*time.Second) {
		removePIDFile()
		fmt.Printf("Daemon stopped (was PID %d)\n", pidData.PID)
		return nil
	}

	fmt.Printf("Daemon PID %d did not exit within 5s; sending SIGKILL...\n", pidData.PID)
	_ = killProcess(proc)
	if !waitForProcessExit(proc, 3*time.Second) {
		// Keep the PID file so the state stays visible for diagnosis.
		return fmt.Errorf("daemon process %d 拒绝退出（SIGKILL 后仍存活），PID 文件已保留，请手动检查", pidData.PID)
	}
	removePIDFile()
	fmt.Printf("Daemon stopped (was PID %d, SIGKILL)\n", pidData.PID)
	return nil
}

// waitForProcessExit polls the process with Signal(0) until it is gone or the
// timeout elapses; reports whether the process exited in time.
func waitForProcessExit(proc *os.Process, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !isProcessAlive(proc) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// daemonProcessMatches verifies via `ps` that the PID recorded in the PID file
// still runs a jcproxy-family binary. Legacy PID files without an exe path
// fall back to matching any jcproxy/JoyCodeProxy command; when ps is
// unavailable the check degrades to "alive is enough" (returns true).
func daemonProcessMatches(data daemonPID) bool {
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(data.PID)).Output()
	if err != nil {
		return true // ps failed/unavailable — don't block shutdown on it
	}
	comm := strings.TrimSpace(string(out))
	if comm == "" {
		return false // ps ran but reported nothing → process is gone
	}
	if data.Exe != "" {
		return strings.Contains(comm, filepath.Base(data.Exe))
	}
	return strings.Contains(comm, "jcproxy") || strings.Contains(comm, "JoyCodeProxy")
}

func daemonStatusCmdRun() error {
	pidData, err := readPIDFile()
	if err != nil {
		fmt.Println("Daemon not running (no PID file).")
		return nil
	}

	proc, err := os.FindProcess(pidData.PID)
	if err != nil {
		fmt.Printf("Daemon PID %d — process lookup failed\n", pidData.PID)
		return nil
	}

	if !isProcessAlive(proc) {
		fmt.Printf("Daemon PID %d — NOT running (stale PID file)\n", pidData.PID)
		removePIDFile()
		return nil
	}

	fmt.Printf("Daemon running (PID %d, port %d, started %s)\n",
		pidData.PID, pidData.Port, pidData.StartedAt)
	fmt.Printf("  Logs: %s\n", daemonLogFile)
	return nil
}

func tailDaemonLogs(n int) error {
	f, err := os.Open(daemonLogFile)
	if err != nil {
		fmt.Println("No daemon log file found.")
		return nil
	}
	defer f.Close()

	// Read only the tail of the file (it can be up to ~100MB) instead of
	// loading the whole thing into memory.
	const tailBytes = 256 * 1024
	size := int64(0)
	if st, err := f.Stat(); err == nil {
		size = st.Size()
	}
	readSize := int64(tailBytes)
	if readSize > size {
		readSize = size
	}
	buf := make([]byte, readSize)
	if _, err := f.ReadAt(buf, size-readSize); err != nil && readSize > 0 {
		fmt.Println("No daemon log file found.")
		return nil
	}
	lines := splitLines(string(buf))
	// The first line of a partial chunk is likely truncated mid-line.
	if readSize < size && len(lines) > 0 {
		lines = lines[1:]
	}
	for _, line := range tailLines(lines, n) {
		fmt.Println(line)
	}
	return nil
}

// tailLines returns the last n entries of lines (nil-safe).
func tailLines(lines []string, n int) []string {
	if n <= 0 || len(lines) == 0 {
		return nil
	}
	start := len(lines) - n
	if start < 0 {
		start = 0
	}
	return lines[start:]
}

// RunSupervisor starts a supervisor loop that spawns and monitors the child process.
func RunSupervisor(port int) {
	home, _ := os.UserHomeDir()
	fullLogDir := filepath.Join(home, logDir)
	cfg := logrot.DefaultConfig(fullLogDir, "daemon")
	rw, err := logrot.New(cfg)
	if err != nil {
		log.Fatalf("[supervisor] cannot open log: %v", err)
	}
	defer rw.Close()
	log.SetOutput(rw)
	slog.SetDefault(slog.New(slog.NewTextHandler(rw, &slog.HandlerOptions{Level: slog.LevelInfo})))

	binPath, err := os.Executable()
	if err == nil {
		log.Printf("[supervisor] starting (PID %d, port %d, exe %s)", os.Getpid(), port, binPath)
	} else {
		log.Printf("[supervisor] starting (PID %d, port %d)", os.Getpid(), port)
		binPath = ""
	}

	writePIDFile(daemonPID{
		PID:       os.Getpid(),
		Port:      port,
		StartedAt: time.Now().Format(time.RFC3339),
		Exe:       binPath,
	})

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	var mu sync.Mutex
	delay := baseRestartDelay
	fastFailures := 0

	for {
		binPath, err := os.Executable()
		if err != nil {
			log.Fatalf("[supervisor] cannot find binary: %v", err)
		}

		args := []string{"serve", "--port", strconv.Itoa(port)}
		if os.Getenv(daemonVerboseEnv) == "1" {
			args = append(args, "-v")
		}
		if os.Getenv(daemonSkipValEnv) == "1" {
			args = append(args, "--skip-validation")
		}
		if host := os.Getenv(daemonHostEnv); host != "" {
			args = append(args, "--host", host)
		}
		if os.Getenv(daemonTLSEnv) == "0" {
			args = append(args, "--tls=false")
		}

		// Build child environment: inherit parent env but REMOVE supervisor marker
		// to prevent infinite fork bomb (child must not think it's a supervisor)
		childEnv := make([]string, 0, len(os.Environ())+1)
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, daemonSupervisorEnv+"=") {
				childEnv = append(childEnv, e)
			}
		}
		childEnv = append(childEnv, daemonChildEnv+"=1")

		cmd := exec.Command(binPath, args...)
		cmd.Env = childEnv
		cmd.Stdout = rw
		cmd.Stderr = rw
		setProcAttrDetached(cmd)

		log.Printf("[supervisor] spawning child process")
		startedAt := time.Now()
		if err := cmd.Start(); err != nil {
			log.Printf("[supervisor] failed to start child: %v", err)
			mu.Lock()
			time.Sleep(delay)
			delay = minDuration(delay*2, maxRestartDelay)
			mu.Unlock()
			continue
		}

		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()

		select {
		case err := <-done:
			ranFor := time.Since(startedAt)
			if err != nil {
				log.Printf("[supervisor] child crashed after %v: %v", ranFor.Round(time.Millisecond), err)
			} else {
				log.Printf("[supervisor] child exited cleanly after %v", ranFor.Round(time.Millisecond))
			}

			// Track consecutive fast failures: a child that dies almost
			// immediately is a deterministic error (bad config, port in use,
			// invalid flags) that restarting won't fix. Give up rather than
			// spin forever.
			if ranFor < fastFailThreshold {
				fastFailures++
				if fastFailures >= maxFastFailures {
					log.Printf("[supervisor] child kept failing fast (%d consecutive times); giving up.", fastFailures)
					log.Printf("[supervisor] check config/port/credentials, then run 'jcproxy daemon restart'.")
					removePIDFile()
					return
				}
			} else {
				fastFailures = 0
				delay = baseRestartDelay
			}

			log.Printf("[supervisor] restarting in %v", delay)
			mu.Lock()
			time.Sleep(delay)
			delay = minDuration(delay*2, maxRestartDelay)
			mu.Unlock()

		case sig := <-sigCh:
			log.Printf("[supervisor] received %v — shutting down", sig)
			terminateProcess(cmd.Process)
			cmd.Wait()
			removePIDFile()
			log.Printf("[supervisor] stopped")
			return
		}
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// --- PID file management ---

type daemonPID struct {
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	StartedAt string `json:"started_at"`
	// Exe is the supervisor binary path recorded at start, used to detect
	// PID reuse (empty in legacy PID files).
	Exe string `json:"exe,omitempty"`
}

func writePIDFile(data daemonPID) error {
	if err := os.MkdirAll(filepath.Dir(daemonPIDFile), 0755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	return os.WriteFile(daemonPIDFile, b, 0644)
}

func readPIDFile() (daemonPID, error) {
	var data daemonPID
	b, err := os.ReadFile(daemonPIDFile)
	if err != nil {
		return data, err
	}
	err = json.Unmarshal(b, &data)
	return data, err
}

func removePIDFile() {
	os.Remove(daemonPIDFile)
}

func checkRunningDaemon() (int, bool) {
	data, err := readPIDFile()
	if err != nil {
		return 0, false
	}
	proc, err := os.FindProcess(data.PID)
	if err != nil {
		return 0, false
	}
	if !isProcessAlive(proc) {
		removePIDFile()
		return 0, false
	}
	return data.PID, true
}
