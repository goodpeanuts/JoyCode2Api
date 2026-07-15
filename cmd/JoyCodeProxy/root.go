package main

import (
	"log"

	"github.com/spf13/cobra"
	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/auth"
	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/joycode"
)

var (
	ptKey          string
	userID         string
	skipValidation bool
	verbose        bool
)

var rootCmd = &cobra.Command{
	Use:   "jcproxy",
	Short: "JoyCode API Proxy — 将 JoyCode API 转换为 OpenAI/Anthropic 兼容格式",
	Long: `JoyCode API Proxy — 将 JoyCode 内部 API 转换为 OpenAI / Anthropic 兼容格式。

让 Claude Code、Codex 等 AI 编程工具可以直接使用 JoyCode 的模型服务。

快速开始:
  jcproxy serve                  # 启动代理服务器（默认端口 34891）
  jcproxy service install        # 安装为 macOS 服务（开机自启、崩溃重启）
  jcproxy check                  # 检查代理是否运行

配置 Claude Code:
  export ANTHROPIC_BASE_URL=http://localhost:34891
  export ANTHROPIC_API_KEY=joycode
  claude`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&ptKey, "ptkey", "k", "", "JoyCode ptKey（留空则自动从客户端检测）")
	rootCmd.PersistentFlags().StringVarP(&userID, "userid", "u", "", "JoyCode userID（留空则自动从客户端检测）")
	rootCmd.PersistentFlags().BoolVar(&skipValidation, "skip-validation", false, "跳过凭据验证")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "启用调试日志")
}

func resolveClient() (*joycode.Client, error) {
	var creds *auth.Credentials
	var source string

	if ptKey != "" && userID != "" {
		creds = &auth.Credentials{PtKey: ptKey, UserID: userID}
		source = "flags"
	} else {
		detected, err := auth.LoadFromSystem()
		if err != nil {
			// No local credentials: start with a placeholder client so the server
			// (and dashboard) still come up. Real requests are served by DB accounts
			// via the per-request resolver, or the user logs in through the dashboard.
			log.Printf("Warning: no JoyCode credentials found (%v)", err)
			log.Printf("  Server will start with a placeholder; open the dashboard to log in or add an account.")
			creds = &auth.Credentials{PtKey: "placeholder", UserID: "placeholder"}
			source = "placeholder (no local JoyCode session)"
		} else {
			creds = detected
			source = "auto-detected"

			if ptKey != "" {
				creds.PtKey = ptKey
				source = "flags+auto-detected"
			}
			if userID != "" {
				creds.UserID = userID
				source = "flags+auto-detected"
			}
		}
	}

	log.Printf("Credentials source: %s (userId=%s)", source, creds.UserID)
	client := joycode.NewClient(creds.PtKey, creds.UserID)
	client.SetColorContext(creds.ColorBaseURL, creds.MasterBaseURL, creds.Tenant, creds.LoginType, creds.OrgFullName)

	if skipValidation {
		log.Printf("Credential validation skipped (--skip-validation)")
		return client, nil
	}
	if creds.PtKey == "placeholder" {
		// Nothing to validate; DB accounts / dashboard login will supply real credentials.
		return client, nil
	}

	log.Printf("Validating credentials...")
	if err := client.Validate(); err != nil {
		// Non-fatal: keep serving so the dashboard stays reachable for re-login.
		// Per-request DB accounts can still override this system client.
		log.Printf("Warning: credential validation failed (%v)", err)
		log.Printf("  Your credentials may have expired — re-log in via the dashboard, or provide fresh --ptkey/--userid.")
		return client, nil
	}
	log.Printf("Credentials validated successfully")
	return client, nil
}
