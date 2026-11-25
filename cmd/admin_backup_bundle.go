package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	bolt "go.etcd.io/bbolt"
)

const (
	adminBundleFormat        = "web2api-full-backup"
	adminBundleFormatVersion = 1
	adminBundleManifestName  = "manifest.json"
	adminBundleConfigName    = "config/config.yaml"
	maxManifestBytes         = 16 << 20
)

type adminBundleEntry struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

type adminBundleManifest struct {
	Format     string             `json:"format"`
	Version    int                `json:"version"`
	CreatedAt  time.Time          `json:"created_at"`
	AppVersion string             `json:"app_version"`
	Entries    []adminBundleEntry `json:"entries"`
}

type adminBundleSource struct {
	Entry  adminBundleEntry
	Source string
}

type stagedAdminBundle struct {
	Root       string
	DataDir    string
	ConfigFile string
	Manifest   adminBundleManifest
}

func runFullAdminBackup(cmd *cobra.Command, dataDir, outPath string) (retErr error) {
	dataRoot, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("解析数据目录失败: %w", err)
	}
	outAbs, err := filepath.Abs(outPath)
	if err != nil {
		return fmt.Errorf("解析备份输出路径失败: %w", err)
	}
	if pathWithin(dataRoot, outAbs) {
		return fmt.Errorf("完整备份输出不能位于数据目录内: %s", outAbs)
	}
	if _, err := os.Lstat(outAbs); err == nil {
		return fmt.Errorf("完整备份输出已存在，请换一个路径: %s", outAbs)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查备份输出路径失败: %w", err)
	}

	sources, configPath, err := collectAdminBundleSources(dataRoot)
	if err != nil {
		return err
	}
	manifest := adminBundleManifest{
		Format:     adminBundleFormat,
		Version:    adminBundleFormatVersion,
		CreatedAt:  time.Now().UTC(),
		AppVersion: version,
		Entries:    make([]adminBundleEntry, 0, len(sources)),
	}
	for _, source := range sources {
		manifest.Entries = append(manifest.Entries, source.Entry)
	}

	if err := writeAdminBundle(outAbs, sources, manifest); err != nil {
		return err
	}
	outInfo, err := os.Stat(outAbs)
	if err != nil {
		return fmt.Errorf("读取完整备份结果失败: %w", err)
	}
	logrus.WithFields(logrus.Fields{
		"data_dir":     dataRoot,
		"config":       configPath,
		"out":          outAbs,
		"bytes":        outInfo.Size(),
		"file_entries": countBundleFiles(manifest.Entries),
	}).Info("完整恢复点备份完成(停服态)")
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "完整备份完成: %s (%d bytes, %d files)\n",
		outAbs, outInfo.Size(), countBundleFiles(manifest.Entries)); err != nil {
		return fmt.Errorf("输出完整备份结果失败: %w", err)
	}
	return nil
}

func collectAdminBundleSources(dataRoot string) ([]adminBundleSource, string, error) {
	info, err := os.Stat(dataRoot)
	if err != nil {
		return nil, "", fmt.Errorf("找不到平台数据目录 %s: %w", dataRoot, err)
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("平台数据目录不是目录: %s", dataRoot)
	}

	configPath, err := resolveAdminConfigFile()
	if err != nil {
		return nil, "", err
	}
	configInfo, err := os.Stat(configPath)
	if err != nil {
		return nil, "", fmt.Errorf("找不到完整备份所需配置文件 %s: %w", configPath, err)
	}
	if !configInfo.Mode().IsRegular() {
		return nil, "", fmt.Errorf("配置文件不是普通文件: %s", configPath)
	}
	if config.IndexJobs.Enabled {
		jobDir := strings.TrimSpace(config.IndexJobs.Dir)
		if jobDir == "" {
			jobDir = filepath.Join(dataRoot, "index_jobs")
		}
		jobAbs, err := filepath.Abs(jobDir)
		if err != nil {
			return nil, "", fmt.Errorf("解析 index_jobs.dir 失败: %w", err)
		}
		if !pathWithin(dataRoot, jobAbs) {
			return nil, "", fmt.Errorf(
				"index_jobs.dir 位于数据目录外(%s)；完整备份拒绝遗漏任务状态，请改用默认数据目录或额外备份该路径",
				jobAbs,
			)
		}
		jobInfo, err := os.Stat(jobAbs)
		if err != nil {
			return nil, "", fmt.Errorf("完整备份要求 index_jobs 目录存在: %w", err)
		}
		if !jobInfo.IsDir() {
			return nil, "", fmt.Errorf("index_jobs.dir 不是目录: %s", jobAbs)
		}
	}

	sources := make([]adminBundleSource, 0, 32)
	err = filepath.WalkDir(dataRoot, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(dataRoot, filePath)
		if err != nil {
			return err
		}
		name := "data"
		if relative != "." {
			name += "/" + filepath.ToSlash(relative)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			return fmt.Errorf("数据目录包含符号链接，拒绝生成可能导致越界读取的备份: %s", filePath)
		case entry.IsDir():
			if !strings.HasSuffix(name, "/") {
				name += "/"
			}
			sources = append(sources, adminBundleSource{
				Source: filePath,
				Entry: adminBundleEntry{
					Path: name,
					Type: "directory",
					Mode: uint32(info.Mode().Perm()),
				},
			})
		case info.Mode().IsRegular():
			sum, size, err := checksumFile(filePath)
			if err != nil {
				return fmt.Errorf("计算备份文件校验和失败 %s: %w", filePath, err)
			}
			sources = append(sources, adminBundleSource{
				Source: filePath,
				Entry: adminBundleEntry{
					Path:   name,
					Type:   "file",
					Mode:   uint32(info.Mode().Perm()),
					Size:   size,
					SHA256: sum,
				},
			})
		default:
			return fmt.Errorf("数据目录包含不支持的文件类型 %s: %s", filePath, info.Mode().Type())
		}
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("扫描数据目录失败: %w", err)
	}

	sum, size, err := checksumFile(configPath)
	if err != nil {
		return nil, "", fmt.Errorf("计算配置文件校验和失败: %w", err)
	}
	sources = append(sources, adminBundleSource{
		Source: configPath,
		Entry: adminBundleEntry{
			Path: adminBundleConfigName,
			Type: "file",
			// Configuration may contain administrator, proxy, or provider secrets.
			Mode:   0o600,
			Size:   size,
			SHA256: sum,
		},
	})
	sort.Slice(sources, func(i, j int) bool { return sources[i].Entry.Path < sources[j].Entry.Path })
	return sources, configPath, nil
}

func writeAdminBundle(outPath string, sources []adminBundleSource, manifest adminBundleManifest) (retErr error) {
	parent := filepath.Dir(outPath)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("备份输出目录不可用: %w", err)
	}
	if !parentInfo.IsDir() {
		return fmt.Errorf("备份输出父路径不是目录: %s", parent)
	}
	tmp, err := os.CreateTemp(parent, "."+filepath.Base(outPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时完整备份失败: %w", err)
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("设置临时备份权限失败: %w", err)
	}

	gzipWriter := gzip.NewWriter(tmp)
	gzipWriter.Name = filepath.Base(outPath)
	gzipWriter.ModTime = manifest.CreatedAt
	tarWriter := tar.NewWriter(gzipWriter)
	closeWriters := func(cause error) error {
		return errors.Join(
			cause,
			wrapIfError("关闭 tar 备份失败", tarWriter.Close()),
			wrapIfError("关闭 gzip 备份失败", gzipWriter.Close()),
			wrapIfError("关闭临时备份文件失败", tmp.Close()),
		)
	}

	for _, source := range sources {
		if err := writeAdminBundleSource(tarWriter, source); err != nil {
			return fmt.Errorf("写入备份条目 %s 失败: %w", source.Entry.Path, closeWriters(err))
		}
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("编码备份清单失败: %w", closeWriters(err))
	}
	manifestJSON = append(manifestJSON, '\n')
	header := &tar.Header{
		Name:     adminBundleManifestName,
		Mode:     0o600,
		Size:     int64(len(manifestJSON)),
		Typeflag: tar.TypeReg,
		ModTime:  manifest.CreatedAt,
		Format:   tar.FormatPAX,
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		return fmt.Errorf("写入备份清单头失败: %w", closeWriters(err))
	}
	if _, err := tarWriter.Write(manifestJSON); err != nil {
		return fmt.Errorf("写入备份清单失败: %w", closeWriters(err))
	}
	if err := tarWriter.Close(); err != nil {
		return fmt.Errorf("完成 tar 备份失败: %w", closeWriters(err))
	}
	if err := gzipWriter.Close(); err != nil {
		return fmt.Errorf("完成 gzip 备份失败: %w", closeWriters(err))
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("同步完整备份失败: %w", closeWriters(err))
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭完整备份失败: %w", err)
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		return fmt.Errorf("发布完整备份失败: %w", err)
	}
	keep = true
	return nil
}

func writeAdminBundleSource(tarWriter *tar.Writer, source adminBundleSource) error {
	header := &tar.Header{
		Name:    source.Entry.Path,
		Mode:    int64(source.Entry.Mode),
		ModTime: time.Now().UTC(),
		Format:  tar.FormatPAX,
	}
	if source.Entry.Type == "directory" {
		header.Typeflag = tar.TypeDir
		header.Name = strings.TrimSuffix(header.Name, "/") + "/"
		return tarWriter.WriteHeader(header)
	}
	file, err := os.Open(source.Source)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return errors.Join(err, file.Close())
	}
	if !info.Mode().IsRegular() || info.Size() != source.Entry.Size {
		return errors.Join(fmt.Errorf("文件在扫描后发生变化"), file.Close())
	}
	header.Typeflag = tar.TypeReg
	header.Size = source.Entry.Size
	header.ModTime = info.ModTime().UTC()
	if err := tarWriter.WriteHeader(header); err != nil {
		return errors.Join(err, file.Close())
	}
	written, copyErr := io.Copy(tarWriter, io.LimitReader(file, source.Entry.Size+1))
	closeErr := file.Close()
	if copyErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if written != source.Entry.Size {
		return fmt.Errorf("文件在扫描后发生变化: 需要 %d 字节，实际写入 %d", source.Entry.Size, written)
	}
	return nil
}

func isAdminBundleArchive(filePath string) (bool, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return false, err
	}
	var magic [2]byte
	n, readErr := io.ReadFull(file, magic[:])
	closeErr := file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return false, errors.Join(readErr, closeErr)
	}
	if closeErr != nil {
		return false, closeErr
	}
	return n == len(magic) && magic == [2]byte{0x1f, 0x8b}, nil
}

func runFullAdminRestore(cmd *cobra.Command, dataDir, inPath string) error {
	dataRoot, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("解析数据目录失败: %w", err)
	}
	configOut, err := resolveFullRestoreConfigPath()
	if err != nil {
		return err
	}
	staged, err := extractAdminBundle(inPath, dataRoot)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staged.Root) }()

	var preDataDir, preConfig string
	if strings.TrimSpace(adminRestorePreserveDir) != "" {
		preDataDir, preConfig, err = applyStagedAdminBundleInPlace(
			staged,
			dataRoot,
			configOut,
			adminRestorePreserveDir,
			adminRestoreKeepCurrentDB,
		)
	} else {
		preDataDir, preConfig, err = applyStagedAdminBundle(staged, dataRoot, configOut, adminRestoreKeepCurrentDB)
	}
	if err != nil {
		return err
	}
	logrus.WithFields(logrus.Fields{
		"data_dir":     dataRoot,
		"config_file":  configOut,
		"from":         inPath,
		"pre_data_dir": preDataDir,
		"pre_config":   preConfig,
	}).Info("完整恢复点恢复完成(停服态)")
	if _, err := fmt.Fprintf(cmd.OutOrStdout(),
		"完整恢复完成: %s 和 %s ← %s\n当前数据留档: %s\n请使用恢复后的配置重启服务。\n",
		dataRoot, configOut, inPath, preDataDir); err != nil {
		return fmt.Errorf("输出完整恢复结果失败: %w", err)
	}
	return nil
}

func extractAdminBundle(inPath, dataRoot string) (result *stagedAdminBundle, retErr error) {
	parent := filepath.Dir(dataRoot)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("创建恢复父目录失败: %w", err)
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(dataRoot)+".restore-*")
	if err != nil {
		return nil, fmt.Errorf("创建隔离恢复目录失败: %w", err)
	}
	keepStage := false
	defer func() {
		if !keepStage {
			retErr = errors.Join(retErr, wrapIfError("清理隔离恢复目录失败", os.RemoveAll(stage)))
		}
	}()

	input, err := os.Open(inPath)
	if err != nil {
		return nil, fmt.Errorf("打开完整备份失败: %w", err)
	}
	gzipReader, err := gzip.NewReader(input)
	if err != nil {
		_ = input.Close()
		return nil, fmt.Errorf("打开 gzip 完整备份失败: %w", err)
	}
	tarReader := tar.NewReader(gzipReader)
	seen := map[string]adminBundleEntry{}
	var manifestSeen bool
	for {
		header, readErr := tarReader.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			closeErr := errors.Join(gzipReader.Close(), input.Close())
			return nil, errors.Join(fmt.Errorf("读取完整备份失败: %w", readErr), closeErr)
		}
		name, nameErr := validateAdminBundlePath(header.Name)
		if nameErr != nil {
			return nil, errors.Join(nameErr, gzipReader.Close(), input.Close())
		}
		entryType := "file"
		switch header.Typeflag {
		case tar.TypeDir:
			entryType = "directory"
		case tar.TypeReg:
		default:
			return nil, errors.Join(
				fmt.Errorf("完整备份包含不支持或链接类型的条目 %q (%d)", name, header.Typeflag),
				gzipReader.Close(), input.Close(),
			)
		}
		if _, exists := seen[name]; exists {
			return nil, errors.Join(
				fmt.Errorf("完整备份包含重复条目 %q", name),
				gzipReader.Close(), input.Close(),
			)
		}
		seen[name] = adminBundleEntry{Path: name, Type: entryType, Size: header.Size}

		if name == adminBundleManifestName {
			if manifestSeen || entryType != "file" || header.Size > maxManifestBytes {
				return nil, errors.Join(fmt.Errorf("完整备份清单大小或类型无效"), gzipReader.Close(), input.Close())
			}
			manifestSeen = true
			payload, readErr := io.ReadAll(io.LimitReader(tarReader, maxManifestBytes+1))
			if readErr != nil {
				return nil, errors.Join(
					fmt.Errorf("读取完整备份清单失败: %w", readErr),
					gzipReader.Close(), input.Close(),
				)
			}
			if int64(len(payload)) != header.Size {
				return nil, errors.Join(fmt.Errorf("完整备份清单长度不一致"), gzipReader.Close(), input.Close())
			}
			if err := os.WriteFile(filepath.Join(stage, adminBundleManifestName), payload, 0o600); err != nil {
				return nil, errors.Join(
					fmt.Errorf("保存完整备份清单失败: %w", err),
					gzipReader.Close(), input.Close(),
				)
			}
			continue
		}

		destination := filepath.Join(stage, filepath.FromSlash(name))
		if entryType == "directory" {
			mode := os.FileMode(header.Mode) & 0o777
			if mode == 0 {
				mode = 0o700
			}
			if err := os.MkdirAll(destination, mode); err != nil {
				return nil, errors.Join(
					fmt.Errorf("创建恢复目录 %q 失败: %w", name, err),
					gzipReader.Close(), input.Close(),
				)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return nil, errors.Join(
				fmt.Errorf("创建恢复父目录失败: %w", err),
				gzipReader.Close(), input.Close(),
			)
		}
		mode := os.FileMode(header.Mode) & 0o777
		if mode == 0 || name == adminBundleConfigName || strings.HasSuffix(name, "/secret.key") {
			mode = 0o600
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return nil, errors.Join(
				fmt.Errorf("创建恢复文件 %q 失败: %w", name, err),
				gzipReader.Close(), input.Close(),
			)
		}
		written, copyErr := io.Copy(output, tarReader)
		syncErr := output.Sync()
		closeErr := output.Close()
		if copyErr != nil || written != header.Size || syncErr != nil || closeErr != nil {
			return nil, errors.Join(
				fmt.Errorf("写入恢复文件 %q 失败: copied=%d expected=%d", name, written, header.Size),
				copyErr, syncErr, closeErr, gzipReader.Close(), input.Close(),
			)
		}
	}
	if err := errors.Join(gzipReader.Close(), input.Close()); err != nil {
		return nil, fmt.Errorf("关闭完整备份读取器失败: %w", err)
	}
	if !manifestSeen {
		return nil, fmt.Errorf("完整备份缺少 manifest.json")
	}

	manifestPath := filepath.Join(stage, adminBundleManifestName)
	manifestPayload, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("读取恢复清单失败: %w", err)
	}
	var manifest adminBundleManifest
	decoder := json.NewDecoder(bytes.NewReader(manifestPayload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("解析恢复清单失败: %w", err)
	}
	if manifest.Format != adminBundleFormat || manifest.Version != adminBundleFormatVersion {
		return nil, fmt.Errorf("不支持的完整备份格式或版本: %q v%d", manifest.Format, manifest.Version)
	}
	if err := verifyStagedAdminBundle(stage, seen, manifest); err != nil {
		return nil, err
	}
	dataDir := filepath.Join(stage, "data")
	configFile := filepath.Join(stage, filepath.FromSlash(adminBundleConfigName))
	dbPath := filepath.Join(dataDir, "web2api.db")
	secretPath := filepath.Join(dataDir, "secret.key")
	if err := validateAdminBundleDatabase(dbPath); err != nil {
		return nil, err
	}
	secretInfo, err := os.Stat(secretPath)
	if err != nil || !secretInfo.Mode().IsRegular() || secretInfo.Size() < 16 {
		return nil, fmt.Errorf("完整备份中的 secret.key 缺失或无效: %v", err)
	}
	configInfo, err := os.Stat(configFile)
	if err != nil || !configInfo.Mode().IsRegular() || configInfo.Size() == 0 {
		return nil, fmt.Errorf("完整备份中的配置文件缺失或为空: %v", err)
	}
	keepStage = true
	return &stagedAdminBundle{
		Root:       stage,
		DataDir:    dataDir,
		ConfigFile: configFile,
		Manifest:   manifest,
	}, nil
}

func verifyStagedAdminBundle(stage string, seen map[string]adminBundleEntry, manifest adminBundleManifest) error {
	expected := make(map[string]adminBundleEntry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		name, err := validateAdminBundlePath(entry.Path)
		if err != nil {
			return fmt.Errorf("备份清单路径无效: %w", err)
		}
		if name == adminBundleManifestName {
			return fmt.Errorf("备份清单不能列出自引用条目")
		}
		if _, duplicate := expected[name]; duplicate {
			return fmt.Errorf("备份清单包含重复条目 %q", name)
		}
		entry.Path = name
		expected[name] = entry
	}
	for name := range seen {
		if name != adminBundleManifestName {
			if _, ok := expected[name]; !ok {
				return fmt.Errorf("备份包含清单外条目 %q", name)
			}
		}
	}
	required := map[string]bool{
		"data/":               false,
		"data/web2api.db":     false,
		"data/secret.key":     false,
		adminBundleConfigName: false,
	}
	for name, entry := range expected {
		if _, ok := required[name]; ok {
			required[name] = true
		}
		actual := seen[name]
		if actual.Path == "" {
			return fmt.Errorf("备份缺少清单条目 %q", name)
		}
		if actual.Type != entry.Type || actual.Size != entry.Size {
			return fmt.Errorf("备份条目 %q 的类型或大小与清单不一致", name)
		}
		filePath := filepath.Join(stage, filepath.FromSlash(name))
		if entry.Type == "directory" {
			info, err := os.Stat(filePath)
			if err != nil || !info.IsDir() {
				return fmt.Errorf("恢复目录 %q 缺失: %v", name, err)
			}
			continue
		}
		if entry.Type != "file" {
			return fmt.Errorf("备份清单条目 %q 的类型无效: %q", name, entry.Type)
		}
		sum, size, err := checksumFile(filePath)
		if err != nil {
			return fmt.Errorf("校验恢复文件 %q 失败: %w", name, err)
		}
		if size != entry.Size || !strings.EqualFold(sum, entry.SHA256) {
			return fmt.Errorf("恢复文件 %q 校验和不一致", name)
		}
	}
	for name, found := range required {
		if !found {
			return fmt.Errorf("完整备份缺少必需条目 %q", name)
		}
	}
	return nil
}

func validateAdminBundleDatabase(dbPath string) error {
	probe, err := bolt.Open(dbPath, 0o400, &bolt.Options{ReadOnly: true, Timeout: 3 * time.Second})
	if err != nil {
		return fmt.Errorf("完整备份中的数据库无法打开: %w", err)
	}
	bucketNames := map[string]bool{"keys": false, "usage": false, "settings": false}
	viewErr := probe.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, _ *bolt.Bucket) error {
			if _, ok := bucketNames[string(name)]; ok {
				bucketNames[string(name)] = true
			}
			return nil
		})
	})
	closeErr := probe.Close()
	if viewErr != nil {
		return fmt.Errorf("校验完整备份数据库失败: %w", viewErr)
	}
	if closeErr != nil {
		return fmt.Errorf("关闭完整备份数据库校验失败: %w", closeErr)
	}
	for name, found := range bucketNames {
		if !found {
			return fmt.Errorf("完整备份数据库缺少契约 bucket %q", name)
		}
	}
	return nil
}

func applyStagedAdminBundle(staged *stagedAdminBundle, dataDir, configOut string, keepCurrent bool) (string, string, error) {
	dataInfo, err := os.Lstat(dataDir)
	if err == nil && !dataInfo.IsDir() {
		return "", "", fmt.Errorf("当前数据路径不是目录: %s", dataDir)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("检查当前数据目录失败: %w", err)
	}
	stamp := time.Now().Format("20060102-150405")
	preDataDir, err := uniqueSiblingPath(dataDir+".pre-restore-"+stamp, dataInfo != nil)
	if err != nil {
		return "", "", err
	}
	configExisted := pathExists(configOut)
	configInsideData := pathWithin(dataDir, configOut)
	preConfigPath := configOut + ".pre-restore-" + stamp
	if configExisted && !configInsideData {
		preConfigPath, err = uniqueSiblingPath(preConfigPath, true)
		if err != nil {
			return "", "", err
		}
	} else if configInsideData {
		relative, relErr := filepath.Rel(dataDir, configOut)
		if relErr != nil {
			return "", "", relErr
		}
		preConfigPath = filepath.Join(preDataDir, relative)
	}
	if configExisted && !configInsideData {
		if err := copyFile(configOut, preConfigPath); err != nil {
			return "", "", fmt.Errorf("保留当前配置文件失败: %w", err)
		}
	}
	dataMoved := false
	if dataInfo != nil {
		if err := os.Rename(dataDir, preDataDir); err != nil {
			if configExisted && !configInsideData {
				_ = os.Remove(preConfigPath)
			}
			return "", "", fmt.Errorf("留档当前数据目录失败: %w", err)
		}
		dataMoved = true
	}
	if err := os.Rename(staged.DataDir, dataDir); err != nil {
		var rollbackErr error
		if dataMoved {
			rollbackErr = os.Rename(preDataDir, dataDir)
		}
		if configExisted && !configInsideData {
			rollbackErr = errors.Join(rollbackErr, os.Remove(preConfigPath))
		}
		return "", "", errors.Join(fmt.Errorf("切换到恢复数据目录失败: %w", err), rollbackErr)
	}
	if err := replaceConfigFromStage(staged.ConfigFile, configOut); err != nil {
		removeNewDataErr := os.RemoveAll(dataDir)
		var restoreDataErr error
		if dataMoved {
			restoreDataErr = os.Rename(preDataDir, dataDir)
		}
		if configExisted && !configInsideData {
			restoreDataErr = errors.Join(restoreDataErr, os.Remove(preConfigPath))
		}
		return "", "", errors.Join(fmt.Errorf("恢复配置文件失败: %w", err), removeNewDataErr, restoreDataErr)
	}
	if !keepCurrent {
		if err := os.RemoveAll(preDataDir); err != nil {
			return preDataDir, preConfigPath, fmt.Errorf("恢复已完成，但清理旧数据目录失败: %w", err)
		}
		if configExisted && !configInsideData {
			if err := os.Remove(preConfigPath); err != nil {
				return preDataDir, preConfigPath, fmt.Errorf("恢复已完成，但清理旧配置文件失败: %w", err)
			}
		}
	}
	if dataInfo == nil {
		preDataDir = "(此前不存在)"
	}
	if !configExisted {
		preConfigPath = "(此前不存在)"
	}
	return preDataDir, preConfigPath, nil
}

func applyStagedAdminBundleInPlace(
	staged *stagedAdminBundle,
	dataDir, configOut, preserveRoot string,
	keepCurrent bool,
) (string, string, error) {
	dataInfo, err := os.Lstat(dataDir)
	if err != nil {
		return "", "", fmt.Errorf("原地恢复要求当前数据目录已挂载: %w", err)
	}
	if !dataInfo.IsDir() {
		return "", "", fmt.Errorf("当前数据路径不是目录: %s", dataDir)
	}
	preserveRootAbs, err := filepath.Abs(preserveRoot)
	if err != nil {
		return "", "", fmt.Errorf("解析保留目录失败: %w", err)
	}
	if pathWithin(dataDir, preserveRootAbs) {
		return "", "", fmt.Errorf("--preserve-in 必须位于数据目录外: %s", preserveRootAbs)
	}
	if err := os.MkdirAll(preserveRootAbs, 0o700); err != nil {
		return "", "", fmt.Errorf("创建保留目录失败: %w", err)
	}
	stamp := time.Now().Format("20060102-150405")
	preDataDir, err := uniqueSiblingPath(
		filepath.Join(preserveRootAbs, filepath.Base(dataDir)+".pre-restore-"+stamp),
		true,
	)
	if err != nil {
		return "", "", err
	}
	if err := copyDirectory(dataDir, preDataDir); err != nil {
		return "", "", fmt.Errorf("复制当前数据目录到保留位置失败: %w", err)
	}

	configExisted := pathExists(configOut)
	configInsideData := pathWithin(dataDir, configOut)
	preConfigPath := configOut + ".pre-restore-" + stamp
	if configExisted && !configInsideData {
		preConfigPath, err = uniqueSiblingPath(preConfigPath, true)
		if err != nil {
			_ = os.RemoveAll(preDataDir)
			return "", "", err
		}
		if err := copyFile(configOut, preConfigPath); err != nil {
			_ = os.RemoveAll(preDataDir)
			return "", "", fmt.Errorf("保留当前配置文件失败: %w", err)
		}
	} else if configInsideData {
		relative, relErr := filepath.Rel(dataDir, configOut)
		if relErr != nil {
			_ = os.RemoveAll(preDataDir)
			return "", "", relErr
		}
		preConfigPath = filepath.Join(preDataDir, relative)
	}

	rollbackData := func(cause error) error {
		removeErr := clearDirectoryContents(dataDir)
		copyErr := copyDirectory(preDataDir, dataDir)
		var configRollback error
		if configExisted && !configInsideData {
			configRollback = copyFile(preConfigPath, configOut)
		}
		return errors.Join(cause, removeErr, copyErr, configRollback)
	}
	if err := clearDirectoryContents(dataDir); err != nil {
		_ = os.RemoveAll(preDataDir)
		if configExisted && !configInsideData {
			_ = os.Remove(preConfigPath)
		}
		return "", "", fmt.Errorf("清空当前数据目录失败: %w", err)
	}
	if err := copyDirectory(staged.DataDir, dataDir); err != nil {
		return "", "", rollbackData(fmt.Errorf("写入原地恢复数据失败: %w", err))
	}
	if err := replaceConfigFromStage(staged.ConfigFile, configOut); err != nil {
		return "", "", rollbackData(fmt.Errorf("恢复配置文件失败: %w", err))
	}
	if !keepCurrent {
		if err := os.RemoveAll(preDataDir); err != nil {
			return preDataDir, preConfigPath, fmt.Errorf("恢复已完成，但清理保留数据失败: %w", err)
		}
		if configExisted && !configInsideData {
			if err := os.Remove(preConfigPath); err != nil {
				return preDataDir, preConfigPath, fmt.Errorf("恢复已完成，但清理保留配置失败: %w", err)
			}
		}
	}
	return preDataDir, preConfigPath, nil
}

func replaceConfigFromStage(staged, destination string) (retErr error) {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".restore-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := copyOpenFile(staged, tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if pathExists(destination) {
		displaced, err := uniqueSiblingPath(destination+".restore-old-"+time.Now().Format("20060102-150405"), true)
		if err != nil {
			return err
		}
		if err := os.Rename(destination, displaced); err != nil {
			return err
		}
		if err := os.Rename(tmpPath, destination); err != nil {
			_ = os.Rename(displaced, destination)
			return err
		}
		_ = os.Remove(displaced)
		return nil
	}
	return os.Rename(tmpPath, destination)
}

func copyOpenFile(srcPath string, dst *os.File) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, src)
	return errors.Join(copyErr, src.Close())
}

func copyDirectory(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("source is not a directory: %s", src)
	}
	if err := os.MkdirAll(dst, srcInfo.Mode().Perm()); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == src {
			return nil
		}
		relative, err := filepath.Rel(src, filePath)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			return fmt.Errorf("数据目录包含符号链接，拒绝复制: %s", filePath)
		case entry.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode().IsRegular():
			if err := copyFile(filePath, target); err != nil {
				return err
			}
			return os.Chmod(target, info.Mode().Perm())
		default:
			return fmt.Errorf("数据目录包含不支持的文件类型 %s: %s", filePath, info.Mode().Type())
		}
	})
}

func clearDirectoryContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func resolveAdminConfigFile() (string, error) {
	for _, candidate := range []string{
		strings.TrimSpace(config.Server.ConfigPath),
		strings.TrimSpace(resolvedConfigFilePath),
	} {
		if candidate == "" {
			continue
		}
		filePath, err := filepath.Abs(candidate)
		if err != nil {
			return "", fmt.Errorf("解析配置文件路径 %s 失败: %w", candidate, err)
		}
		return filePath, nil
	}
	return "", fmt.Errorf("完整备份需要 config.yaml: 请用 -c/--config 指定当前服务实际使用的配置文件")
}

func resolveFullRestoreConfigPath() (string, error) {
	candidate := strings.TrimSpace(adminRestoreConfigOut)
	if candidate == "" {
		var err error
		candidate, err = resolveAdminConfigFile()
		if err != nil {
			return "", fmt.Errorf("无法确定恢复后的配置路径: %w；可指定 --config-out", err)
		}
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("解析恢复配置路径失败: %w", err)
	}
	if pathExists(absolute) {
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return "", fmt.Errorf("解析恢复配置符号链接失败: %w", err)
		}
		absolute, err = filepath.Abs(resolved)
		if err != nil {
			return "", fmt.Errorf("解析恢复配置目标路径失败: %w", err)
		}
	}
	return absolute, nil
}

func checksumFile(filePath string) (string, int64, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return "", 0, errors.Join(copyErr, closeErr)
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func validateAdminBundlePath(name string) (string, error) {
	if strings.Contains(name, "\\") || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("备份条目路径包含非法字符: %q", name)
	}
	clean := path.Clean(strings.TrimSuffix(name, "/"))
	if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("备份条目路径越界: %q", name)
	}
	if clean != adminBundleManifestName &&
		clean != adminBundleConfigName &&
		clean != "data" &&
		!strings.HasPrefix(clean, "data/") {
		return "", fmt.Errorf("备份条目不在允许的路径内: %q", name)
	}
	if strings.HasSuffix(name, "/") && clean != adminBundleManifestName && clean != adminBundleConfigName {
		clean += "/"
	}
	return clean, nil
}

func pathWithin(root, target string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func countBundleFiles(entries []adminBundleEntry) int {
	count := 0
	for _, entry := range entries {
		if entry.Type == "file" {
			count++
		}
	}
	return count
}

func pathExists(filePath string) bool {
	_, err := os.Lstat(filePath)
	return err == nil
}

func uniqueSiblingPath(basePath string, needed bool) (string, error) {
	if !needed {
		return basePath, nil
	}
	for i := 0; i < 1000; i++ {
		candidate := basePath
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", basePath, i)
		}
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("无法为恢复留档生成唯一路径: %s", basePath)
}
