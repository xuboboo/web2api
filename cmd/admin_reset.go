package cmd

// S4-GAP 会话系统: 密码找回 CLI (本机停服态重置管理员哈希,无邮件依赖).
//
//	web2api admin-reset                      # 交互式输入新密码(两次确认)
//	web2api admin-reset --password <new>     # 脚本化重置
//	可选 --data-dir / --username
//
// 直接改写 settings 桶里的 admin_password_hash 并写入 admin_password_custom
// 标记(与面板「修改密码」同一条路径的落盘形态),随后重启服务生效。运行前
// 必须停服: bbolt 打开锁与 serve 实例互斥(fail-closed),否则本命令报错退出。

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
)

var adminResetCMD = &cobra.Command{
	Use:   "admin-reset",
	Short: "Reset the admin password directly in the store (server must be stopped)",
	RunE:  runAdminReset,
}

var (
	adminResetPassword string
	adminResetDataDir  string
	adminResetUsername string
)

func runAdminReset(cmd *cobra.Command, args []string) error {
	dataDir := adminResetDataDir
	if dataDir == "" {
		dataDir = config.Platform.DataDir
	}
	username := adminResetUsername
	if username == "" {
		username = config.Platform.AdminUsername
	}

	newPassword := adminResetPassword
	if newPassword == "" {
		// Interactive prompt with a confirmation round. 不引新依赖(单二进制
		// 纪律):终端回显未关闭;需要无回显输入时可改用管道:
		//   printf '新密码' | web2api admin-reset
		fmt.Fprint(cmd.OutOrStdout(), "请输入新密码(提示:回显未关闭,可改用管道输入): ")
		first, err := readPasswordLine()
		if err != nil {
			return fmt.Errorf("读取密码失败: %w", err)
		}
		fmt.Fprint(cmd.OutOrStdout(), "请再次输入确认: ")
		second, err := readPasswordLine()
		if err != nil {
			return fmt.Errorf("读取密码失败: %w", err)
		}
		if first != second {
			return fmt.Errorf("两次输入不一致,未做任何修改")
		}
		newPassword = first
	}
	newPassword = strings.TrimSpace(newPassword)
	if n := len([]rune(newPassword)); n < 8 {
		return fmt.Errorf("密码长度至少需要 8 个字符(当前 %d),未做任何修改", n)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("生成哈希失败: %w", err)
	}

	dbPath := filepath.Join(dataDir, "web2api.db")
	db, err := bolt.Open(dbPath, 0o600, &bolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		return fmt.Errorf("打开 %s 失败(请先停止 serve 实例): %w", dbPath, err)
	}
	defer db.Close()

	err = db.Update(func(tx *bolt.Tx) error {
		settings := tx.Bucket([]byte("settings"))
		if settings == nil {
			return fmt.Errorf("settings bucket 不存在,该文件不是本平台的数据库")
		}
		if err := settings.Put([]byte("admin_password_hash"), hash); err != nil {
			return err
		}
		return settings.Put([]byte("admin_password_custom"), []byte("1"))
	})
	if err != nil {
		return fmt.Errorf("写入新哈希失败: %w", err)
	}

	logrus.WithField("username", username).Info("管理员密码已在停服态重置")
	fmt.Fprintf(cmd.OutOrStdout(), "管理员 %s 的密码已重置(数据库 %s)。重启服务后生效。\n", username, dbPath)
	return nil
}

// stdinLineReader shares one buffered reader across both password prompts —
// two separate bufio.Readers would each drain the pipe buffer and the second
// read would see EOF.
var stdinLineReader = bufio.NewReader(os.Stdin)

// readPasswordLine reads one line from stdin (tty or piped). Echo-free input
// would need a terminal-control dependency, which the single-binary rule
// excludes; piping is the recommended no-echo path.
func readPasswordLine() (string, error) {
	line, err := stdinLineReader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func init() {
	RootCmd.AddCommand(adminResetCMD)
	adminResetCMD.Flags().StringVar(&adminResetPassword, "password", "", "新密码(省略则交互式输入)")
	adminResetCMD.Flags().StringVar(&adminResetDataDir, "data-dir", "", "平台数据目录(默认取 platform.data_dir)")
	adminResetCMD.Flags().StringVar(&adminResetUsername, "username", "", "管理员用户名(默认取 platform.admin_username)")
}
