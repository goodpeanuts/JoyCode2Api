package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	uninstallPurge      bool
	uninstallYes        bool
	uninstallKeepBinary bool
)

var uninstallCmd = &cobra.Command{
	Use:     "uninstall",
	Short:   "卸载 jcproxy（停止服务、移除二进制）",
	Long: `停止后台守护进程与系统服务，并删除已安装的二进制文件。

默认保留 ~/.joycode-proxy 数据目录（账号凭据、日志、数据库）。
如需彻底清除数据，加 --purge --yes。`,
	GroupID: "service",
	Example: `  # 停止服务并删除二进制（保留数据）
  jcproxy uninstall

  # 彻底卸载，连同数据一起删除
  jcproxy uninstall --purge --yes

  # 只停止服务，不删二进制
  jcproxy uninstall --keep-binary`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUninstall()
	},
}

func init() {
	uninstallCmd.Flags().BoolVar(&uninstallPurge, "purge", false, "同时删除 ~/.joycode-proxy 数据目录（需配合 --yes）")
	uninstallCmd.Flags().BoolVar(&uninstallYes, "yes", false, "跳过确认，直接执行危险操作（如 --purge）")
	uninstallCmd.Flags().BoolVar(&uninstallKeepBinary, "keep-binary", false, "不删除二进制文件，仅停止服务")
	rootCmd.AddCommand(uninstallCmd)
}

// runUninstall 停止后台进程/服务、可选清除数据、并自删二进制。
// 每一步都容错：单步失败只打印警告，不中断后续步骤。
func runUninstall() error {
	fmt.Println("正在卸载 jcproxy...")

	// 1. 停止守护进程（幂等，未运行时会优雅提示）
	fmt.Println("→ 停止守护进程")
	if err := stopDaemon(); err != nil {
		fmt.Printf("  警告：停止守护进程时出错：%v\n", err)
	}

	// 2. 卸载系统服务（幂等，未安装时会优雅提示）
	fmt.Println("→ 移除系统服务")
	if err := uninstallService(); err != nil {
		fmt.Printf("  警告：移除系统服务时出错：%v\n", err)
	}

	// 3. 数据目录：默认保留，仅 --purge --yes 才删除
	home, homeErr := os.UserHomeDir()
	dataDir := ""
	if homeErr == nil {
		dataDir = filepath.Join(home, ".joycode-proxy")
	}
	if uninstallPurge {
		if !uninstallYes {
			fmt.Println("→ --purge 需配合 --yes 才会删除数据，已跳过。数据保留在:", dataDir)
		} else if dataDir != "" {
			fmt.Println("→ 删除数据目录:", dataDir)
			if err := os.RemoveAll(dataDir); err != nil {
				fmt.Printf("  警告：删除数据目录失败：%v\n", err)
			}
		}
	} else if dataDir != "" {
		fmt.Println("→ 数据目录已保留:", dataDir)
		fmt.Println("  （如需彻底清除账号与数据，重新执行并加 --purge --yes）")
	}

	// 4. 自删二进制（默认执行，--keep-binary 跳过）
	if uninstallKeepBinary {
		fmt.Println("→ 已保留二进制（--keep-binary）")
	} else {
		binPath, err := os.Executable()
		if err != nil {
			fmt.Printf("  警告：无法定位二进制路径，请手动删除：%v\n", err)
		} else {
			if resolved, rerr := filepath.EvalSymlinks(binPath); rerr == nil {
				binPath = resolved
			}
			fmt.Println("→ 删除二进制:", binPath)
			if err := os.Remove(binPath); err != nil {
				fmt.Printf("  警告：删除二进制失败，请手动删除 %s：%v\n", binPath, err)
			}
		}
	}

	fmt.Println("卸载完成。")
	return nil
}
