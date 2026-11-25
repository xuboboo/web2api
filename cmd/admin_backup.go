package cmd

// S4-GAP 运维: CLI backup/restore for the platform store, run with the server
// STOPPED (停服态). The API-side hot backup lives at GET /api/admin/backup.
//
//	web2api admin-backup --out <file.db> [--data-dir ./data]
//	web2api admin-backup --full --out <bundle.tar.gz> --data-dir ./data
//	web2api admin-restore --in <file.db|bundle.tar.gz> [--data-dir ./data]
//
// The restore validates the snapshot by opening it as a bbolt database (and
// checking the contract buckets exist) before touching web2api.db, keeps a
// timestamped copy of the current database beside it, and swaps atomically via
// rename. Full bundles additionally contain the complete data directory and
// the resolved configuration file; corrupt or unexpected archive entries are
// rejected before either destination is changed.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	bolt "go.etcd.io/bbolt"
)

var adminBackupCMD = &cobra.Command{
	Use:   "admin-backup",
	Short: "Backup the platform store to a snapshot file (server must be stopped)",
	RunE:  runAdminBackup,
}

var adminRestoreCMD = &cobra.Command{
	Use:   "admin-restore",
	Short: "Restore the platform store from a snapshot file (server must be stopped)",
	RunE:  runAdminRestore,
}

var (
	adminBackupOut            string
	adminRestoreIn            string
	adminDataDir              string
	adminRestoreConfigOut     string
	adminRestorePreserveDir   string
	adminBackupFull           bool
	adminRestoreKeepCurrentDB bool
)

func resolveAdminDataDir() string {
	if adminDataDir != "" {
		return adminDataDir
	}
	return config.Platform.DataDir
}

func runAdminBackup(cmd *cobra.Command, args []string) (retErr error) {
	dataDir := resolveAdminDataDir()
	dbPath := filepath.Join(dataDir, "web2api.db")
	outPath := adminBackupOut
	if outPath == "" {
		outPath = fmt.Sprintf("web2api-backup-%s.db", time.Now().Format("20060102-150405"))
	}
	if adminBackupFull {
		if adminBackupOut == "" {
			outPath = fmt.Sprintf("web2api-backup-%s.tar.gz", time.Now().Format("20060102-150405"))
		}
		return runFullAdminBackup(cmd, dataDir, outPath)
	}
	dbInfo, err := os.Stat(dbPath)
	if err != nil {
		return fmt.Errorf("找不到平台数据库 %s: %w", dbPath, err)
	}
	if outInfo, statErr := os.Stat(outPath); statErr == nil {
		if os.SameFile(dbInfo, outInfo) {
			return fmt.Errorf("备份输出路径不能指向源数据库 %s", dbPath)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("检查备份输出路径失败: %w", statErr)
	}

	// Snapshot through a read-only open so the copy is transaction-consistent.
	src, err := bolt.Open(dbPath, 0o600, &bolt.Options{ReadOnly: true, Timeout: 3 * time.Second})
	if err != nil {
		return fmt.Errorf("打开数据库失败: %w", err)
	}
	defer func() {
		if closeErr := src.Close(); closeErr != nil && retErr == nil {
			retErr = fmt.Errorf("关闭源数据库失败: %w", closeErr)
		}
	}()

	dst, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("创建备份文件失败: %w", err)
	}
	dstClosed := false
	defer func() {
		if !dstClosed {
			if closeErr := dst.Close(); closeErr != nil && retErr == nil {
				retErr = fmt.Errorf("关闭备份文件失败: %w", closeErr)
			}
		}
	}()
	var written int64
	copyErr := src.View(func(tx *bolt.Tx) error {
		n, txErr := tx.WriteTo(dst)
		written = n
		return txErr
	})
	if copyErr != nil {
		closeErr := dst.Close()
		dstClosed = true
		removeErr := os.Remove(outPath)
		return errors.Join(
			fmt.Errorf("写备份失败: %w", copyErr),
			wrapIfError("关闭不完整备份失败", closeErr),
			wrapIfError("删除不完整备份失败", removeErr),
		)
	}
	if err := dst.Sync(); err != nil {
		closeErr := dst.Close()
		dstClosed = true
		removeErr := os.Remove(outPath)
		return errors.Join(
			fmt.Errorf("同步备份文件失败: %w", err),
			wrapIfError("关闭不完整备份失败", closeErr),
			wrapIfError("删除不完整备份失败", removeErr),
		)
	}
	if err := dst.Close(); err != nil {
		dstClosed = true
		removeErr := os.Remove(outPath)
		return errors.Join(
			fmt.Errorf("关闭备份文件失败: %w", err),
			wrapIfError("删除不完整备份失败", removeErr),
		)
	}
	dstClosed = true
	logrus.WithFields(logrus.Fields{"db": dbPath, "out": outPath, "bytes": written}).
		Info("平台数据备份完成(停服态)")
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "备份完成: %s (%d bytes)\n", outPath, written); err != nil {
		return fmt.Errorf("输出备份结果失败: %w", err)
	}
	return nil
}

func wrapIfError(message string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
}

func runAdminRestore(cmd *cobra.Command, args []string) error {
	dataDir := resolveAdminDataDir()
	dbPath := filepath.Join(dataDir, "web2api.db")
	inPath := adminRestoreIn
	if inPath == "" {
		return fmt.Errorf("请通过 --in 指定备份文件")
	}
	isBundle, err := isAdminBundleArchive(inPath)
	if err != nil {
		return fmt.Errorf("读取备份文件类型失败: %w", err)
	}
	if isBundle {
		return runFullAdminRestore(cmd, dataDir, inPath)
	}

	// Validate the snapshot first: it must open as bbolt and carry the
	// contract buckets (plus the S4 ones). JSON-fallback stores are not
	// restored by this command (they are a single editable file).
	probe, err := bolt.Open(inPath, 0o400, &bolt.Options{ReadOnly: true, Timeout: 3 * time.Second})
	if err != nil {
		return fmt.Errorf("备份文件无法作为 bbolt 数据库打开: %w", err)
	}
	var bucketNames []string
	err = probe.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, _ *bolt.Bucket) error {
			bucketNames = append(bucketNames, string(name))
			return nil
		})
	})
	closeErr := probe.Close()
	if err != nil {
		return fmt.Errorf("备份文件校验失败: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("关闭备份校验文件失败: %w", closeErr)
	}
	required := map[string]bool{"keys": false, "usage": false, "settings": false}
	for _, name := range bucketNames {
		if _, ok := required[name]; ok {
			required[name] = true
		}
	}
	for name, found := range required {
		if !found {
			return fmt.Errorf("备份文件缺少契约 bucket %q,拒绝恢复", name)
		}
	}

	if _, err := os.Stat(dbPath); err == nil && adminRestoreKeepCurrentDB {
		kept := fmt.Sprintf("%s.pre-restore-%s", dbPath, time.Now().Format("20060102-150405"))
		if err := copyFile(dbPath, kept); err != nil {
			return fmt.Errorf("保留当前数据库失败: %w", err)
		}
		logrus.WithField("kept", kept).Info("当前数据库已留档")
	}

	// Copy into a sibling temp file, then swap atomically via rename.
	tmp := dbPath + ".restore.tmp"
	if err := copyFile(inPath, tmp); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := os.Rename(tmp, dbPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换数据库失败: %w", err)
	}
	logrus.WithFields(logrus.Fields{"db": dbPath, "from": inPath}).Info("平台数据恢复完成(停服态)")
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "恢复完成: %s ← %s\n请重启服务生效。\n", dbPath, inPath); err != nil {
		return fmt.Errorf("输出恢复结果失败: %w", err)
	}
	return nil
}

func copyFile(src, dst string) (retErr error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, in.Close()) }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, out.Close()) }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func init() {
	RootCmd.AddCommand(adminBackupCMD)
	RootCmd.AddCommand(adminRestoreCMD)
	adminBackupCMD.Flags().StringVar(&adminBackupOut, "out", "", "备份输出文件(默认 web2api-backup-<时间戳>.db)")
	adminBackupCMD.Flags().StringVar(&adminDataDir, "data-dir", "", "平台数据目录(默认取 platform.data_dir)")
	adminBackupCMD.Flags().BoolVar(&adminBackupFull, "full", false, "生成包含整个数据目录和当前配置文件的完整 tar.gz 备份")
	adminRestoreCMD.Flags().StringVar(&adminRestoreIn, "in", "", "备份文件路径")
	adminRestoreCMD.Flags().StringVar(&adminDataDir, "data-dir", "", "平台数据目录(默认取 platform.data_dir)")
	adminRestoreCMD.Flags().StringVar(&adminRestoreConfigOut, "config-out", "", "完整备份恢复后的配置文件路径(默认使用当前 -c 或自动发现的配置)")
	adminRestoreCMD.Flags().StringVar(&adminRestorePreserveDir, "preserve-in", "", "挂载卷根目录恢复时，将原数据复制到此目录并原地回滚切换")
	adminRestoreCMD.Flags().BoolVar(&adminRestoreKeepCurrentDB, "backup-current", true, "恢复前把当前数据库留档为 *.pre-restore-<时间戳>")
}
