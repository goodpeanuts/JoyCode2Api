package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const (
	serviceLabel   = "com.joycode.proxy"
	plistName      = serviceLabel + ".plist"
	logDir         = ".joycode-proxy/logs"
	serviceCfgFile = ".joycode-proxy/service.json"
)

// serviceConfig records what `service install` was configured with, so
// `jcproxy update` can reinstall the service with the same settings instead
// of heuristically parsing the plist/unit text.
type serviceConfig struct {
	Port           int    `json:"port"`
	SkipValidation bool   `json:"skip_validation,omitempty"`
	BinaryPath     string `json:"binary_path,omitempty"`
}

func writeServiceConfig(cfg serviceConfig) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(home, serviceCfgFile)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(path, b, 0644)
}

func removeServiceConfig() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	os.Remove(filepath.Join(home, serviceCfgFile))
}

// readServiceConfig returns the persisted install configuration, if any.
func readServiceConfig() (serviceConfig, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return serviceConfig{}, false
	}
	b, err := os.ReadFile(filepath.Join(home, serviceCfgFile))
	if err != nil {
		return serviceConfig{}, false
	}
	var cfg serviceConfig
	if err := json.Unmarshal(b, &cfg); err != nil || cfg.Port <= 0 {
		return serviceConfig{}, false
	}
	return cfg, true
}

var serviceCmd = &cobra.Command{
	Use:     "service",
	Short:   "管理后台服务（安装/卸载/状态）",
	Long:    "将 JoyCode Proxy 安装为系统后台服务，支持开机自启和崩溃自动重启。自动适配 macOS (launchd)、Linux (systemd) 和 Windows (NSSM)。",
	GroupID: "service",
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "安装并启动后台服务",
	Long:  "将代理安装为系统后台服务。安装后自动启动，支持开机自启和崩溃自动重启。",
	Example: `  # 使用默认端口 34891 安装
  jcproxy service install

  # 指定端口
  jcproxy service install -p 8080`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := serviceConfig{
			Port:           servePort,
			SkipValidation: serviceSkipValidation,
		}
		writeServiceConfig(cfg)
		return installService(cfg)
	},
}

var serviceUninstallCmd = &cobra.Command{
	Use:     "uninstall",
	Short:   "停止并移除后台服务",
	Example: `  jcproxy service uninstall`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return uninstallService()
	},
}

var serviceStatusCmd = &cobra.Command{
	Use:     "status",
	Short:   "查看服务运行状态",
	Example: `  jcproxy service status`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return serviceStatus()
	},
}

var serviceSkipValidation bool

func init() {
	serviceCmd.AddCommand(serviceInstallCmd)
	serviceCmd.AddCommand(serviceUninstallCmd)
	serviceCmd.AddCommand(serviceStatusCmd)
	serviceInstallCmd.Flags().BoolVar(&serviceSkipValidation, "skip-validation", false, "服务启动时跳过凭据校验（与 serve 同名旗标一致）")
	serviceCmd.PersistentFlags().IntVarP(&servePort, "port", "p", 34891, "绑定端口")
	rootCmd.AddCommand(serviceCmd)
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			lines = append(lines, line)
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
