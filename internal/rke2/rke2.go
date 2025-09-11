package rke2

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rainbond/rainbond-offline-installer/pkg/config"
)

// Logger 定义日志接口
type Logger interface {
	Debug(format string, v ...interface{})
	Info(format string, v ...interface{})
	Warn(format string, v ...interface{})
	Error(format string, v ...interface{})
}

// StepProgress 进度接口
type StepProgress interface {
	StartSubSteps(totalSubSteps int)
	StartSubStep(subStepName string)
	CompleteSubStep()
	CompleteSubSteps()
	StartNodeProcessing(nodeIP string)
	CompleteNodeStep(nodeIP string)
}

const (
	RKE2DefaultToken = "9L1wA2hTP3DmqYf3eDSeWB4J"
	RKE2ConfigDir    = "/etc/rancher/rke2"
	RKE2ConfigFile   = "/etc/rancher/rke2/config.yaml"
	RKE2CustomConfig = "/etc/rancher/rke2/config.yaml.d/00-rbd.yaml"
)

// FileArtifact 文件传输配置
type FileArtifact struct {
	localPath  string
	remotePath string
	required   bool
}

type RKE2Installer struct {
	config       *config.Config
	logger       Logger
	stepProgress StepProgress
}

type RKE2Status struct {
	IP       string
	Role     []string
	Status   string
	Running  bool
	IsServer bool
	IsAgent  bool
	Error    string
}

func NewRKE2Installer(cfg *config.Config) *RKE2Installer {
	return NewRKE2InstallerWithLogger(cfg, nil)
}

func NewRKE2InstallerWithLogger(cfg *config.Config, logger Logger) *RKE2Installer {
	return NewRKE2InstallerWithLoggerAndProgress(cfg, logger, nil)
}

func NewRKE2InstallerWithLoggerAndProgress(cfg *config.Config, logger Logger, stepProgress StepProgress) *RKE2Installer {
	return &RKE2Installer{
		config:       cfg,
		logger:       logger,
		stepProgress: stepProgress,
	}
}

func (r *RKE2Installer) Run() error {
	if r.logger != nil {
		r.logger.Info("开始RKE2 Kubernetes集群安装...")
	}

	// 检查RKE2配置
	hosts := r.config.Hosts
	if len(hosts) == 0 {
		return fmt.Errorf("未找到主机配置，无法安装RKE2")
	}

	// 获取各类型节点
	etcdHosts := r.getEtcdHosts()
	masterHosts := r.getMasterHosts()
	workerHosts := r.getAgentHosts()
	firstEtcdHost := r.getFirstEtcdHost()

	if firstEtcdHost == nil {
		return fmt.Errorf("至少需要配置一个etcd或master节点作为第一个节点")
	}

	if r.logger != nil {
		r.logger.Info("发现RKE2配置: %d个etcd节点, %d个master节点, %d个worker节点",
			len(etcdHosts), len(masterHosts), len(workerHosts))
	}

	// 调试信息：显示节点分类详情
	if r.logger != nil {
		r.logger.Debug("第一个etcd节点: %s (角色: %v)", firstEtcdHost.IP, firstEtcdHost.Role)
	}
	if r.logger != nil {
		r.logger.Debug("etcd节点列表:")
	}
	for i, host := range etcdHosts {
		if r.logger != nil {
			r.logger.Debug("  etcd #%d: %s (角色: %v)", i+1, host.IP, host.Role)
		}
	}
	if r.logger != nil {
		r.logger.Debug("master节点列表:")
	}
	for i, host := range masterHosts {
		if r.logger != nil {
			r.logger.Debug("  master #%d: %s (角色: %v)", i+1, host.IP, host.Role)
		}
	}
	if r.logger != nil {
		r.logger.Debug("worker节点列表:")
	}
	for i, host := range workerHosts {
		if r.logger != nil {
			r.logger.Debug("  worker #%d: %s (角色: %v)", i+1, host.IP, host.Role)
		}
	}

	// 阶段1: 检查当前状态
	if r.logger != nil {
		r.logger.Debug("=== 阶段1: 检查RKE2状态 ===")
	}
	status := r.checkRKE2Status()
	r.printRKE2Status(status)

	// 检查是否所有节点都已经在运行
	runningCount := 0
	installedCount := 0
	for _, s := range status {
		if s.Status == "运行中" {
			runningCount++
			installedCount++
		} else if s.Status == "已安装未运行" || s.Status == "服务运行中但节点未就绪" {
			installedCount++
		}
	}

	// 如果所有节点都已经运行，跳过安装阶段
	if runningCount == len(hosts) {
		if r.logger != nil {
			r.logger.Info("检测到所有 %d 个节点的RKE2服务都已经运行中，跳过安装阶段", len(hosts))
		}
		if r.logger != nil {
			r.logger.Info("=== 直接进行最终验证 ===")
		}

		// 验证集群状态
		if err := r.waitForClusterReady(*firstEtcdHost); err != nil {
			if r.logger != nil {
				r.logger.Warn("集群就绪检查失败: %v，但节点已在运行，继续完成", err)
			}
		}

		if r.logger != nil {
			r.logger.Info("RKE2集群已完成! 运行中: %d/%d", runningCount, len(hosts))
		}
		return nil
	}

	// 如果有部分节点需要安装，继续执行安装流程
	if r.logger != nil {
		r.logger.Info("检测到部分节点需要安装或启动: 运行中 %d/%d, 已安装 %d/%d", runningCount, len(hosts), installedCount, len(hosts))
	}

	// 阶段2: 传输离线资源到所有节点
	if r.logger != nil {
		r.logger.Debug("=== 阶段2: 传输离线资源到所有节点 ===")
	}
	if err := r.transferOfflineResourcesToAllNodes(); err != nil {
		return fmt.Errorf("传输离线资源失败: %w", err)
	}

	// 阶段3: 验证所有节点的安装包完整性
	if r.logger != nil {
		r.logger.Debug("=== 阶段3: 验证安装包完整性 ===")
	}
	if err := r.validatePackageIntegrityOnAllNodes(); err != nil {
		return fmt.Errorf("安装包完整性验证失败: %w", err)
	}

	// 阶段4: 顺序安装RKE2服务
	if r.logger != nil {
		r.logger.Debug("=== 阶段4: 安装RKE2服务 ===")
	}

	// 步骤1: 安装第一个etcd节点（必须包含etcd）
	if r.stepProgress != nil {
		r.stepProgress.StartNodeProcessing(firstEtcdHost.IP)
	}
	if r.logger != nil {
		r.logger.Info("开始安装第一个节点: %s (角色: %s)", firstEtcdHost.IP, firstEtcdHost.Role)
	}
	if err := r.installRKE2OnServer(*firstEtcdHost, true); err != nil {
		return fmt.Errorf("第一个节点 %s RKE2安装失败: %w", firstEtcdHost.IP, err)
	}
	if r.stepProgress != nil {
		r.stepProgress.CompleteNodeStep(firstEtcdHost.IP)
	}

	// 等待第一个etcd节点启动
	if r.logger != nil {
		r.logger.Info("第一个节点安装完成，等待服务就绪...")
	}
	if err := r.waitForServerReady(*firstEtcdHost); err != nil {
		return fmt.Errorf("等待第一个etcd节点 %s 就绪失败: %w", firstEtcdHost.IP, err)
	}
	if r.logger != nil {
		r.logger.Info("第一个节点已就绪，开始安装其他节点...")
	}

	// 步骤2: 安装其他etcd节点
	if r.logger != nil {
		r.logger.Debug("检查其他etcd节点，第一个节点是: %s", firstEtcdHost.IP)
	}
	etcdCount := 0
	for _, etcdHost := range etcdHosts {
		if r.logger != nil {
			r.logger.Debug("检查etcd节点: %s，是否等于第一个节点: %v", etcdHost.IP, etcdHost.IP == firstEtcdHost.IP)
		}
		if etcdHost.IP == firstEtcdHost.IP {
			continue // 跳过第一个节点
		}
		etcdCount++
		
		if r.stepProgress != nil {
			r.stepProgress.StartNodeProcessing(etcdHost.IP)
		}
		if r.logger != nil {
			r.logger.Info("安装etcd节点: %s (角色: %v)", etcdHost.IP, etcdHost.Role)
		}
		if err := r.installRKE2OnServer(etcdHost, false); err != nil {
			return fmt.Errorf("etcd节点 %s RKE2安装失败: %w", etcdHost.IP, err)
		}
		if r.stepProgress != nil {
			r.stepProgress.CompleteNodeStep(etcdHost.IP)
		}
		if r.logger != nil {
			r.logger.Info("etcd节点 %s 安装完成", etcdHost.IP)
		}
	}
	if etcdCount == 0 {
		if r.logger != nil {
			r.logger.Info("没有其他etcd节点需要安装")
		}
	} else {
		if r.logger != nil {
			r.logger.Info("完成 %d 个其他etcd节点的安装", etcdCount)
		}
	}

	// 步骤3: 安装专用master节点（control-plane）
	masterCount := 0
	for _, masterHost := range masterHosts {
		if masterHost.IP == firstEtcdHost.IP {
			continue // 跳过第一个节点（如果它已经是master）
		}
		masterCount++
		
		if r.stepProgress != nil {
			r.stepProgress.StartNodeProcessing(masterHost.IP)
		}
		if r.logger != nil {
			r.logger.Info("安装master节点: %s (角色: %s)", masterHost.IP, masterHost.Role)
		}
		if err := r.installRKE2OnServer(masterHost, false); err != nil {
			return fmt.Errorf("master节点 %s RKE2安装失败: %w", masterHost.IP, err)
		}
		if r.stepProgress != nil {
			r.stepProgress.CompleteNodeStep(masterHost.IP)
		}
		if r.logger != nil {
			r.logger.Info("master节点 %s 安装完成", masterHost.IP)
		}
	}
	if masterCount == 0 {
		if r.logger != nil {
			r.logger.Info("没有其他master节点需要安装")
		}
	}

	// 步骤4: 安装worker节点
	if r.logger != nil {
		r.logger.Info("开始安装 %d 个worker节点...", len(workerHosts))
	}
	for i, workerHost := range workerHosts {
		if r.stepProgress != nil {
			r.stepProgress.StartNodeProcessing(workerHost.IP)
		}
		if r.logger != nil {
			r.logger.Info("安装worker节点 %d/%d: %s", i+1, len(workerHosts), workerHost.IP)
		}
		if err := r.installRKE2OnAgent(workerHost); err != nil {
			return fmt.Errorf("worker节点 %s RKE2安装失败: %w", workerHost.IP, err)
		}
		if r.stepProgress != nil {
			r.stepProgress.CompleteNodeStep(workerHost.IP)
		}
		if r.logger != nil {
			r.logger.Info("worker节点 %s 安装完成", workerHost.IP)
		}
	}
	if len(workerHosts) == 0 {
		if r.logger != nil {
			r.logger.Info("没有worker节点需要安装")
		}
	}

	// 阶段5: 等待集群就绪
	if r.logger != nil {
		r.logger.Debug("=== 阶段5: 等待集群就绪 ===")
	}
	if err := r.waitForClusterReady(*firstEtcdHost); err != nil {
		return fmt.Errorf("等待集群就绪失败: %w", err)
	}

	// 阶段6: 等待所有节点服务稳定
	if r.logger != nil {
		r.logger.Debug("=== 阶段6: 等待所有节点服务稳定 ===")
	}
	if r.logger != nil {
		r.logger.Info("监控RKE2服务状态，等待所有节点就绪...")
	}

	// 主动监控节点状态，最多等待120秒
	maxWaitTime := 120
	checkInterval := 10 // 每10秒检查一次

	for elapsed := 0; elapsed < maxWaitTime; elapsed += checkInterval {
		if r.logger != nil {
			r.logger.Info("检查节点状态... (已等待 %d/%d 秒)", elapsed, maxWaitTime)
		}

		// 检查当前状态
		currentStatus := r.checkRKE2Status()

		// 统计运行中的节点
		runningCount := 0
		installedCount := 0
		for _, s := range currentStatus {
			if s.Status == "运行中" {
				runningCount++
				installedCount++
			} else if s.Status == "已安装未运行" {
				installedCount++
			}
		}

		if r.logger != nil {
			r.logger.Info("当前状态: 已安装 %d/%d, 运行中 %d/%d", installedCount, len(hosts), runningCount, len(hosts))
		}

		// 如果所有节点都在运行，提前结束等待
		if runningCount == len(hosts) {
			if r.logger != nil {
				r.logger.Info("所有节点已就绪，提前结束等待")
			}
			break
		}

		// 如果还没到最大等待时间，继续等待
		if elapsed+checkInterval < maxWaitTime {
			if r.logger != nil {
				r.logger.Info("等待 %d 秒后重新检查...", checkInterval)
			}
			time.Sleep(time.Duration(checkInterval) * time.Second)
		}
	}

	// 阶段7: 最终状态验证
	if r.logger != nil {
		r.logger.Debug("=== 阶段7: 验证安装结果 ===")
	}
	finalStatus := r.checkRKE2Status()
	r.printRKE2Status(finalStatus)

	// 检查安装成功率
	finalRunningCount := 0
	finalInstalledCount := 0
	failedHosts := []string{}

	for _, s := range finalStatus {
		if s.Status == "运行中" {
			finalRunningCount++
			finalInstalledCount++
		} else if s.Status == "已安装未运行" {
			finalInstalledCount++
			if r.logger != nil {
				r.logger.Warn("节点 %s: RKE2已安装但服务未运行，可能仍在启动中", s.IP)
			}
		} else if s.Status == "未安装" {
			failedHosts = append(failedHosts, s.IP)
		}
	}

	if r.logger != nil {
		r.logger.Info("RKE2集群安装完成! 已安装: %d/%d, 运行中: %d/%d", finalInstalledCount, len(hosts), finalRunningCount, len(hosts))
	}

	if finalInstalledCount < len(hosts) {
		if r.logger != nil {
			r.logger.Error("以下节点安装失败: %v", failedHosts)
		}
		if r.logger != nil {
			r.logger.Error("建议检查:")
		}
		if r.logger != nil {
			r.logger.Error("  1. 网络连接是否正常")
		}
		if r.logger != nil {
			r.logger.Error("  2. 系统资源是否充足")
		}
		if r.logger != nil {
			r.logger.Error("  3. 执行 journalctl -u rke2-server -f 查看日志")
		}
		if r.logger != nil {
			r.logger.Error("  4. 重新执行: roi install --rke2 --config config.yaml")
		}
		return fmt.Errorf("部分RKE2节点安装失败，失败节点: %v", failedHosts)
	}

	if finalRunningCount < len(hosts) {
		notRunningCount := finalInstalledCount - finalRunningCount
		if r.logger != nil {
			r.logger.Warn("注意: %d个节点已安装但服务未运行，这可能是正常的启动延迟", notRunningCount)
		}
		if r.logger != nil {
			r.logger.Info("建议等待几分钟后检查服务状态: systemctl status rke2-server 或 rke2-agent")
		}
	}

	return nil
}

// normalizeRoles 标准化角色数组，转换为小写
func (r *RKE2Installer) normalizeRoles(roles []string) []string {
	var normalizedRoles []string
	for _, role := range roles {
		role = strings.TrimSpace(strings.ToLower(role))
		if role != "" {
			normalizedRoles = append(normalizedRoles, role)
		}
	}
	return normalizedRoles
}

// hasRole 检查角色列表中是否包含指定角色
func (r *RKE2Installer) hasRole(roles []string, targetRole string) bool {
	for _, role := range roles {
		if role == targetRole {
			return true
		}
	}
	return false
}

// getServerHosts 获取server角色的主机（master和etcd节点）
func (r *RKE2Installer) getServerHosts() []config.Host {
	var servers []config.Host
	for _, host := range r.config.Hosts {
		// 标准化角色数组
		roles := r.normalizeRoles(host.Role)
		// 如果包含master或etcd角色，则作为RKE2 server安装
		if r.hasRole(roles, "master") || r.hasRole(roles, "etcd") {
			servers = append(servers, host)
		}
	}
	return servers
}

// getAgentHosts 获取agent角色的主机（纯worker节点，不包括server节点）
func (r *RKE2Installer) getAgentHosts() []config.Host {
	var agents []config.Host
	for _, host := range r.config.Hosts {
		roles := r.normalizeRoles(host.Role)
		// 只有纯worker节点才作为agent安装（不包含master或etcd角色的worker节点）
		if r.hasRole(roles, "worker") && !r.hasRole(roles, "master") && !r.hasRole(roles, "etcd") {
			agents = append(agents, host)
		}
	}
	return agents
}

// getFirstEtcdHost 获取第一个etcd节点（必须是集群中的第一个节点）
func (r *RKE2Installer) getFirstEtcdHost() *config.Host {
	for _, host := range r.config.Hosts {
		roles := r.normalizeRoles(host.Role)
		if r.hasRole(roles, "etcd") || r.hasRole(roles, "master") {
			return &host
		}
	}
	return nil
}

// getEtcdHosts 获取所有etcd节点
func (r *RKE2Installer) getEtcdHosts() []config.Host {
	var etcdHosts []config.Host
	for _, host := range r.config.Hosts {
		roles := r.normalizeRoles(host.Role)
		if r.hasRole(roles, "etcd") || r.hasRole(roles, "master") {
			etcdHosts = append(etcdHosts, host)
		}
	}
	return etcdHosts
}

// getMasterHosts 获取所有master节点（专用control-plane节点）
func (r *RKE2Installer) getMasterHosts() []config.Host {
	var masterHosts []config.Host
	for _, host := range r.config.Hosts {
		roles := r.normalizeRoles(host.Role)
		if r.hasRole(roles, "master") {
			masterHosts = append(masterHosts, host)
		}
	}
	return masterHosts
}

// installRKE2OnServer 在server节点安装RKE2
func (r *RKE2Installer) installRKE2OnServer(host config.Host, isFirstServer bool) error {
	if r.logger != nil {
		r.logger.Info("主机 %s: 开始安装RKE2 server", host.IP)
	}

	// 步骤0: 检查是否已经安装
	if installed, err := r.checkRKE2Installed(host); err != nil {
		if r.logger != nil {
			r.logger.Warn("主机 %s: 检查RKE2安装状态失败: %v", host.IP, err)
		}
	} else if installed {
		if r.logger != nil {
			r.logger.Info("主机 %s: RKE2已安装，检查并启动服务", host.IP)
		}

		// 确保RKE2服务正在运行
		if err := r.startRKE2Service(host, "server"); err != nil {
			if r.logger != nil {
				r.logger.Warn("主机 %s: 启动RKE2服务失败: %v", host.IP, err)
			}
		}

		// 如果是第一个server节点，仍需配置kubectl和检查状态
		if isFirstServer {
			if err := r.configureKubectl(host); err != nil {
				return fmt.Errorf("配置kubectl失败: %w", err)
			}
			if err := r.waitForNodeReady(host); err != nil {
				return fmt.Errorf("等待节点就绪失败: %w", err)
			}
		}
		return nil
	}

	// 步骤1: 生成RKE2配置文件（离线资源已在前期阶段传输完成）
	if err := r.createRKE2Config(host, "server", isFirstServer); err != nil {
		return fmt.Errorf("创建RKE2配置失败: %w", err)
	}

	// 步骤2: 执行RKE2安装脚本
	if err := r.executeRKE2Install(host, "server"); err != nil {
		return fmt.Errorf("执行RKE2安装失败: %w", err)
	}

	// 步骤3: 启动RKE2服务
	if err := r.startRKE2Service(host, "server"); err != nil {
		return fmt.Errorf("启动RKE2服务失败: %w", err)
	}

	// 步骤4: 如果是第一个server节点，配置kubectl并等待节点就绪
	if isFirstServer {
		if err := r.configureKubectl(host); err != nil {
			return fmt.Errorf("配置kubectl失败: %w", err)
		}

		if err := r.waitForNodeReady(host); err != nil {
			return fmt.Errorf("等待节点就绪失败: %w", err)
		}
	}

	if r.logger != nil {
		r.logger.Info("主机 %s: RKE2 server安装完成", host.IP)
	}
	return nil
}

// installRKE2OnAgent 在agent节点安装RKE2
func (r *RKE2Installer) installRKE2OnAgent(host config.Host) error {
	if r.logger != nil {
		r.logger.Info("主机 %s: 开始安装RKE2 agent", host.IP)
	}

	// 步骤0: 检查是否已经安装
	if installed, err := r.checkRKE2Installed(host); err != nil {
		if r.logger != nil {
			r.logger.Warn("主机 %s: 检查RKE2安装状态失败: %v", host.IP, err)
		}
	} else if installed {
		if r.logger != nil {
			r.logger.Info("主机 %s: RKE2已安装，检查并启动服务", host.IP)
		}

		// 确保RKE2服务正在运行
		if err := r.startRKE2Service(host, "agent"); err != nil {
			if r.logger != nil {
				r.logger.Warn("主机 %s: 启动RKE2服务失败: %v", host.IP, err)
			}
		}
		return nil
	}

	// 步骤1: 生成RKE2配置文件（离线资源已在前期阶段传输完成）
	if err := r.createRKE2Config(host, "agent", false); err != nil {
		return fmt.Errorf("创建RKE2配置失败: %w", err)
	}

	// 步骤2: 执行RKE2安装脚本
	if err := r.executeRKE2Install(host, "agent"); err != nil {
		return fmt.Errorf("执行RKE2安装失败: %w", err)
	}

	// 步骤3: 启动RKE2服务
	if err := r.startRKE2Service(host, "agent"); err != nil {
		return fmt.Errorf("启动RKE2服务失败: %w", err)
	}

	if r.logger != nil {
		r.logger.Info("主机 %s: RKE2 agent安装完成", host.IP)
	}
	return nil
}

// createRKE2Directories 创建RKE2目录结构
func (r *RKE2Installer) createRKE2Directories(host config.Host) error {
	if r.logger != nil {
		r.logger.Info("主机 %s: 创建RKE2目录结构", host.IP)
	}

	createDirsCmd := fmt.Sprintf(`
		# 创建RKE2配置目录
		mkdir -p %s
		mkdir -p %s/config.yaml.d
		
		# 创建RKE2数据目录
		mkdir -p /var/lib/rancher/rke2
		
		# 创建日志目录
		mkdir -p /var/log/rke2
		
		echo "RKE2目录创建完成"
	`, RKE2ConfigDir, RKE2ConfigDir)

	sshCmd := r.buildSSHCommand(host, createDirsCmd)
	output, err := sshCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("创建RKE2目录失败: %w, 输出: %s", err, string(output))
	}

	return nil
}

// transferRKE2Artifacts 传输RKE2离线资源文件
func (r *RKE2Installer) transferRKE2Artifacts(host config.Host) error {
	if r.logger != nil {
		r.logger.Info("主机 %s: 开始传输RKE2离线资源文件", host.IP)
	}

	// 定义需要传输的文件
	artifacts := []FileArtifact{
		{"rke2-install.sh", "/tmp/rke2-artifacts/rke2-install.sh", true},
		{"rke2.linux*.tar.gz", "/tmp/rke2-artifacts/rke2.linux*.tar.gz", true},
		{"sha256sum*.txt", "/tmp/rke2-artifacts/sha256sum*.txt", true},
		{"rke2-images-linux.tar", "/var/lib/rancher/rke2/agent/images/rke2-images.linux.tar", true},
		{"rainbond-offline-images.tar", "/var/lib/rancher/rke2/agent/images/rainbond-offline-images.tar", true},
	}

	// 创建远程目录
	createDirsCmd := `
		mkdir -p /tmp/rke2-artifacts
		mkdir -p /var/lib/rancher/rke2/agent/images
		echo "RKE2离线资源目录创建完成"
	`

	sshCmd := r.buildSSHCommand(host, createDirsCmd)
	if err := sshCmd.Run(); err != nil {
		return fmt.Errorf("创建RKE2离线资源目录失败: %w", err)
	}

	// 传输每个文件
	for _, artifact := range artifacts {
		if err := r.transferArtifact(host, artifact); err != nil {
			if artifact.required {
				return fmt.Errorf("传输必需文件 %s 失败: %w", artifact.localPath, err)
			}
			if r.logger != nil {
				r.logger.Warn("主机 %s: 传输可选文件 %s 失败: %v", host.IP, artifact.localPath, err)
			}
		}
	}

	// 设置脚本执行权限
	chmodCmd := `chmod +x /tmp/rke2-artifacts/rke2-install.sh`
	sshCmd = r.buildSSHCommand(host, chmodCmd)
	if err := sshCmd.Run(); err != nil {
		return fmt.Errorf("设置RKE2安装脚本执行权限失败: %w", err)
	}

	if r.logger != nil {
		r.logger.Info("主机 %s: RKE2离线资源文件传输完成", host.IP)
	}
	return nil
}

// transferArtifact 传输文件，支持通配符模式
func (r *RKE2Installer) transferArtifact(host config.Host, artifact FileArtifact) error {
	// 检查是否包含通配符
	if strings.Contains(artifact.localPath, "*") {
		return r.transferWildcardFiles(host, artifact.localPath, artifact.remotePath)
	}

	// 普通文件传输
	return r.transferFileWithProgress(host, artifact.localPath, artifact.remotePath)
}

// transferWildcardFiles 传输通配符匹配的文件
func (r *RKE2Installer) transferWildcardFiles(host config.Host, localPattern, remotePattern string) error {
	// 使用glob查找匹配的文件
	matches, err := filepath.Glob(localPattern)
	if err != nil {
		return fmt.Errorf("通配符模式 %s 匹配失败: %w", localPattern, err)
	}

	if len(matches) == 0 {
		return fmt.Errorf("本地文件 %s 不存在", localPattern)
	}

	// 传输每个匹配的文件
	for _, localFile := range matches {
		// 计算对应的远程文件名，直接使用文件名替换通配符
		fileName := filepath.Base(localFile)
		remoteDir := filepath.Dir(remotePattern)
		remoteFile := filepath.Join(remoteDir, fileName)

		if r.logger != nil {
			r.logger.Info("主机 %s: 通配符匹配到文件: %s -> %s", host.IP, localFile, remoteFile)
		}

		if err := r.transferFileWithProgress(host, localFile, remoteFile); err != nil {
			return fmt.Errorf("传输文件 %s 失败: %w", localFile, err)
		}
	}

	return nil
}

// addLocalFileInfos 添加本地文件信息，支持通配符
func (r *RKE2Installer) addLocalFileInfos(artifact FileArtifact, fileInfos map[string]*FileInfo) error {
	// 检查是否包含通配符
	if strings.Contains(artifact.localPath, "*") {
		// 处理通配符模式
		matches, err := filepath.Glob(artifact.localPath)
		if err != nil {
			return fmt.Errorf("通配符模式 %s 匹配失败: %w", artifact.localPath, err)
		}

		if len(matches) == 0 {
			return fmt.Errorf("本地文件 %s 不存在", artifact.localPath)
		}

		// 为每个匹配的文件添加信息
		for _, localFile := range matches {
			info, err := r.getLocalFileInfo(localFile)
			if err != nil {
				return fmt.Errorf("处理通配符文件 %s (匹配 %s): %w", localFile, artifact.localPath, err)
			}

			// 使用实际文件名作为key
			fileName := filepath.Base(localFile)
			remoteDir := filepath.Dir(artifact.remotePath)
			remoteFile := filepath.Join(remoteDir, fileName)
			fileInfos[remoteFile] = info
		}

		return nil
	}

	// 普通文件处理
	info, err := r.getLocalFileInfo(artifact.localPath)
	if err != nil {
		return err
	}

	fileInfos[artifact.remotePath] = info
	return nil
}

// validateFilesOnHost 验证单个主机上的文件完整性
func (r *RKE2Installer) validateFilesOnHost(host config.Host, artifacts []FileArtifact, localFileInfos map[string]*FileInfo) error {
	for _, artifact := range artifacts {
		if !artifact.required {
			continue
		}

		if strings.Contains(artifact.localPath, "*") {
			// 处理通配符文件验证
			if err := r.validateWildcardFiles(host, artifact, localFileInfos); err != nil {
				return err
			}
		} else {
			// 处理普通文件验证
			localInfo := localFileInfos[artifact.remotePath]
			if localInfo == nil {
				return fmt.Errorf("未找到本地文件 %s 的信息", artifact.localPath)
			}

			remoteInfo, err := r.getRemoteFileInfo(host, artifact.remotePath)
			if err != nil {
				return fmt.Errorf("获取远程文件 %s 信息失败: %w", artifact.remotePath, err)
			}

			// 验证文件大小和MD5
			if remoteInfo.size != localInfo.size || remoteInfo.md5 != localInfo.md5 {
				return fmt.Errorf("文件 %s 校验失败: 预期大小=%d MD5=%s, 实际大小=%d MD5=%s",
					artifact.remotePath, localInfo.size, localInfo.md5, remoteInfo.size, remoteInfo.md5)
			}

			if r.logger != nil {
				r.logger.Debug("节点 %s: 文件 %s 校验通过", host.IP, artifact.remotePath)
			}
		}
	}

	return nil
}

// validateWildcardFiles 验证通配符文件
func (r *RKE2Installer) validateWildcardFiles(host config.Host, artifact FileArtifact, localFileInfos map[string]*FileInfo) error {
	// 查找所有匹配的本地文件
	matches, err := filepath.Glob(artifact.localPath)
	if err != nil {
		return fmt.Errorf("通配符模式 %s 匹配失败: %w", artifact.localPath, err)
	}

	if len(matches) == 0 {
		return fmt.Errorf("本地文件 %s 不存在", artifact.localPath)
	}

	// 验证每个匹配的文件
	for _, localFile := range matches {
		fileName := filepath.Base(localFile)
		remoteDir := filepath.Dir(artifact.remotePath)
		remoteFile := filepath.Join(remoteDir, fileName)

		localInfo := localFileInfos[remoteFile]
		if localInfo == nil {
			return fmt.Errorf("未找到本地文件 %s 的信息", localFile)
		}

		remoteInfo, err := r.getRemoteFileInfo(host, remoteFile)
		if err != nil {
			return fmt.Errorf("获取远程文件 %s 信息失败: %w", remoteFile, err)
		}

		// 验证文件大小和MD5
		if remoteInfo.size != localInfo.size || remoteInfo.md5 != localInfo.md5 {
			return fmt.Errorf("文件 %s 校验失败: 预期大小=%d MD5=%s, 实际大小=%d MD5=%s",
				remoteFile, localInfo.size, localInfo.md5, remoteInfo.size, remoteInfo.md5)
		}

		if r.logger != nil {
			r.logger.Debug("节点 %s: 文件 %s 校验通过", host.IP, remoteFile)
		}
	}

	return nil
}

// transferFileWithProgress 智能传输文件，支持完整性校验和断点续传
func (r *RKE2Installer) transferFileWithProgress(host config.Host, localPath, remotePath string) error {
	if r.logger != nil {
		r.logger.Info("主机 %s: 开始传输 %s -> %s", host.IP, localPath, remotePath)
	}

	// 检查本地文件是否存在
	if _, err := exec.Command("test", "-f", localPath).Output(); err != nil {
		return fmt.Errorf("本地文件 %s 不存在", localPath)
	}

	// 获取本地文件信息和MD5
	localInfo, err := r.getLocalFileInfo(localPath)
	if err != nil {
		return fmt.Errorf("获取本地文件信息失败: %w", err)
	}

	if r.logger != nil {
		r.logger.Info("主机 %s: 本地文件 %s (大小: %s, MD5: %s)",
			host.IP, localPath, localInfo.sizeHuman, localInfo.md5[:8]+"...")
	}

	// 检查远程文件是否已存在且完整
	remoteInfo, err := r.getRemoteFileInfo(host, remotePath)
	if err == nil && remoteInfo.size == localInfo.size && remoteInfo.md5 == localInfo.md5 {
		if r.logger != nil {
			r.logger.Info("主机 %s: 远程文件已存在且完整，跳过传输", host.IP)
		}
		return nil
	}

	if err == nil && remoteInfo.size > 0 {
		if r.logger != nil {
			r.logger.Info("主机 %s: 发现不完整的远程文件 (大小: %s, MD5: %s)，将重新传输",
				host.IP, remoteInfo.sizeHuman, remoteInfo.md5[:8]+"...")
		}
	}

	// 传输文件
	if err := r.transferFileWithScp(host, localPath, remotePath); err != nil {
		return fmt.Errorf("文件传输失败: %w", err)
	}

	// 验证传输后的文件完整性
	finalInfo, err := r.getRemoteFileInfo(host, remotePath)
	if err != nil {
		return fmt.Errorf("验证传输后文件失败: %w", err)
	}

	if finalInfo.size != localInfo.size || finalInfo.md5 != localInfo.md5 {
		return fmt.Errorf("文件传输后校验失败: 预期大小=%d MD5=%s, 实际大小=%d MD5=%s",
			localInfo.size, localInfo.md5, finalInfo.size, finalInfo.md5)
	}

	if r.logger != nil {
		r.logger.Info("主机 %s: 文件传输成功并校验通过: %s", host.IP, localPath)
	}
	return nil
}

// transferFileWithScp 使用scp或rsync传输文件，优先rsync以支持进度条
func (r *RKE2Installer) transferFileWithScp(host config.Host, localPath, remotePath string) error {
	if r.logger != nil {
		r.logger.Info("主机 %s: 开始传输 %s", host.IP, localPath)
	}

	// 首先尝试使用rsync (支持进度条)
	if err := r.transferFileWithRsync(host, localPath, remotePath); err == nil {
		return nil
	}

	// rsync失败时回退到scp
	if r.logger != nil {
		r.logger.Info("主机 %s: rsync不可用，使用scp传输", host.IP)
	}
	scpCmd := r.buildScpCommand(host, localPath, remotePath)
	output, err := scpCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("scp传输失败: %w, 输出: %s", err, string(output))
	}

	if r.logger != nil {
		r.logger.Info("主机 %s: scp传输完成", host.IP)
	}
	return nil
}

// transferFileWithRsync 使用rsync传输文件（支持进度条）
func (r *RKE2Installer) transferFileWithRsync(host config.Host, localPath, remotePath string) error {
	// 检查rsync是否可用
	if _, err := exec.LookPath("rsync"); err != nil {
		return fmt.Errorf("rsync不可用: %w", err)
	}

	if r.logger != nil {
		r.logger.Info("主机 %s: 使用rsync传输 %s (显示进度)", host.IP, localPath)
	}

	var rsyncCmd *exec.Cmd
	target := fmt.Sprintf("%s@%s:%s", host.User, host.IP, remotePath)

	// 构建rsync命令参数
	baseArgs := []string{
		"--progress",       // 显示传输进度
		"--human-readable", // 人类可读的大小格式
		"--compress",       // 启用压缩传输
		"--partial",        // 支持断点续传
		"--inplace",        // 就地更新文件
		"--stats",          // 显示传输统计信息
	}

	// 根据认证方式构建SSH命令
	if host.Password != "" {
		if _, err := exec.LookPath("sshpass"); err == nil {
			sshOpts := "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
			rsyncCmd = exec.Command("sshpass", "-p", host.Password, "rsync")
			args := append(baseArgs, "-e", sshOpts, localPath, target)
			rsyncCmd.Args = append(rsyncCmd.Args, args...)
		} else {
			return fmt.Errorf("需要sshpass工具来支持密码认证的rsync")
		}
	} else if host.SSHKey != "" {
		sshOpts := fmt.Sprintf("ssh -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null", host.SSHKey)
		args := append(baseArgs, "-e", sshOpts, localPath, target)
		rsyncCmd = exec.Command("rsync", args...)
	} else {
		sshOpts := "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
		args := append(baseArgs, "-e", sshOpts, localPath, target)
		rsyncCmd = exec.Command("rsync", args...)
	}

	// 抑制输出，避免干扰进度显示
	// rsyncCmd.Stdout = os.Stdout
	// rsyncCmd.Stderr = os.Stderr

	if err := rsyncCmd.Run(); err != nil {
		return fmt.Errorf("rsync传输失败: %w", err)
	}

	if r.logger != nil {
		r.logger.Info("主机 %s: rsync传输完成", host.IP)
	}
	return nil
}

// FileInfo 文件信息结构体
type FileInfo struct {
	size      int64
	sizeHuman string
	md5       string
}

// getLocalFileInfo 获取本地文件信息
func (r *RKE2Installer) getLocalFileInfo(filePath string) (*FileInfo, error) {
	// 先检查文件是否存在
	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("本地文件 %s 不存在", filePath)
		}
		return nil, fmt.Errorf("访问本地文件 %s 失败: %w", filePath, err)
	}

	// 获取文件大小 - 兼容Linux和macOS
	var sizeInt int64
	var sizeHuman string

	// 尝试Linux的stat命令
	statCmd := exec.Command("stat", "-c", "%s", filePath)
	sizeOutput, err := statCmd.Output()
	if err != nil {
		// 尝试macOS的stat命令
		statCmd = exec.Command("stat", "-f", "%z", filePath)
		sizeOutput, err = statCmd.Output()
		if err != nil {
			return nil, fmt.Errorf("获取文件大小失败 (Linux和macOS stat都失败): %w", err)
		}
	}

	size := strings.TrimSpace(string(sizeOutput))
	fmt.Sscanf(size, "%d", &sizeInt)

	// 获取人类可读的文件大小
	statHumanCmd := exec.Command("ls", "-lh", filePath)
	statHumanOutput, err := statHumanCmd.Output()
	if err != nil {
		sizeHuman = "未知"
	} else {
		fields := strings.Fields(string(statHumanOutput))
		if len(fields) >= 5 {
			sizeHuman = fields[4]
		} else {
			sizeHuman = "未知"
		}
	}

	// 计算MD5 - 兼容Linux和macOS
	var md5Hash string
	md5Cmd := exec.Command("md5sum", filePath)
	md5Output, err := md5Cmd.Output()
	if err != nil {
		// 尝试macOS的md5命令
		md5Cmd = exec.Command("md5", "-q", filePath)
		md5Output, err = md5Cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("计算MD5失败: %w", err)
		}
		md5Hash = strings.TrimSpace(string(md5Output))
	} else {
		md5Hash = strings.Fields(string(md5Output))[0]
	}

	return &FileInfo{
		size:      sizeInt,
		sizeHuman: sizeHuman,
		md5:       md5Hash,
	}, nil
}

// getRemoteFileInfo 获取远程文件信息
func (r *RKE2Installer) getRemoteFileInfo(host config.Host, filePath string) (*FileInfo, error) {
	// 检查文件是否存在并获取大小和MD5
	checkCmd := fmt.Sprintf(`
		if [ -f "%s" ]; then
			echo "EXISTS"
			stat -c "%%s" "%s" 2>/dev/null || echo "0"
			ls -lh "%s" 2>/dev/null | awk '{print $5}' || echo "0"
			md5sum "%s" 2>/dev/null | awk '{print $1}' || echo "no_md5"
		else
			echo "NOT_EXISTS"
		fi
	`, filePath, filePath, filePath, filePath)

	sshCmd := r.buildSSHCommand(host, checkCmd)
	output, err := sshCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("检查远程文件失败: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 1 || lines[0] != "EXISTS" {
		return nil, fmt.Errorf("远程文件不存在")
	}

	if len(lines) < 4 {
		return nil, fmt.Errorf("获取远程文件信息不完整")
	}

	size := int64(0)
	fmt.Sscanf(lines[1], "%d", &size)
	sizeHuman := lines[2]
	md5Hash := lines[3]

	if md5Hash == "no_md5" || md5Hash == "" {
		return nil, fmt.Errorf("无法获取远程文件MD5")
	}

	return &FileInfo{
		size:      size,
		sizeHuman: sizeHuman,
		md5:       md5Hash,
	}, nil
}

// getNodeConfigSection 生成节点配置部分
func (r *RKE2Installer) getNodeConfigSection(host config.Host) string {
	nodeName := r.getNodeName(host)

	configLines := []string{
		fmt.Sprintf("# 节点配置"),
		fmt.Sprintf("node-name: %s", nodeName),
	}

	// node-ip 使用internal_ip（现在是必填字段）
	configLines = append(configLines, fmt.Sprintf("node-ip: %s", host.InternalIP))

	// node-external-ip 使用ip，仅当ip和internal_ip不同时才配置
	if host.IP != host.InternalIP {
		configLines = append(configLines, fmt.Sprintf("node-external-ip: %s", host.IP))
	}

	// node-taint 配置污点 - 智能调度策略
	taints := r.getRecommendedTaints(host)
	if len(taints) > 0 {
		configLines = append(configLines, "# 节点污点配置 - 智能调度策略")
		configLines = append(configLines, "node-taint:")
		for _, taint := range taints {
			configLines = append(configLines, fmt.Sprintf("  - \"%s\"", taint))
		}
	}

	return strings.Join(configLines, "\n")
}

// getRecommendedTaints 根据集群组成和节点角色推荐合适的污点配置
func (r *RKE2Installer) getRecommendedTaints(host config.Host) []string {
	roles := r.normalizeRoles(host.Role)
	isControlPlane := r.hasRole(roles, "etcd") || r.hasRole(roles, "master")
	
	// 如果用户明确配置了NodeTaint，先检查是否可能导致问题
	if len(host.NodeTaint) > 0 {
		if r.logger != nil {
			r.logger.Info("主机 %s: 检测到用户配置的污点: %v", host.IP, host.NodeTaint)
		}
		
		// 检查是否使用了可能导致系统组件调度问题的污点
		for _, taint := range host.NodeTaint {
			if strings.Contains(taint, ":NoSchedule") && isControlPlane {
				hasWorkers := r.hasWorkerNodes()
				if !hasWorkers {
					if r.logger != nil {
						r.logger.Warn("主机 %s: 检测到NoSchedule污点但集群中没有worker节点，这可能导致系统组件无法调度", host.IP)
						r.logger.Warn("建议: 在只有master/etcd节点的集群中，建议使用PreferNoSchedule或标准的control-plane污点")
					}
				}
			}
		}
		
		// 使用用户配置的污点
		return host.NodeTaint
	}
	
	// 如果没有用户配置的污点，根据集群组成自动推荐
	if !isControlPlane {
		// 非控制平面节点不需要污点
		return []string{}
	}
	
	hasWorkers := r.hasWorkerNodes()
	var recommendedTaints []string
	
	if hasWorkers {
		// 有worker节点的情况：使用标准的control-plane污点
		// 这个污点被大多数系统组件的tolerations支持
		recommendedTaints = []string{
			"node-role.kubernetes.io/control-plane:NoSchedule",
		}
		if r.logger != nil {
			r.logger.Info("主机 %s: 集群有worker节点，为控制平面节点配置标准污点以避免业务负载调度", host.IP)
		}
	} else {
		// 只有控制平面节点的情况：使用更宽松的策略
		// 使用PreferNoSchedule允许必要时调度到控制平面节点
		recommendedTaints = []string{
			"node-role.kubernetes.io/control-plane:PreferNoSchedule",
		}
		if r.logger != nil {
			r.logger.Info("主机 %s: 集群只有控制平面节点，使用PreferNoSchedule策略以确保系统组件能够正常调度", host.IP)
		}
	}
	
	return recommendedTaints
}

// hasWorkerNodes 检查集群中是否有worker节点
func (r *RKE2Installer) hasWorkerNodes() bool {
	for _, host := range r.config.Hosts {
		roles := r.normalizeRoles(host.Role)
		// 如果有纯worker角色的节点，或者有包含worker但不包含etcd/master的节点
		if r.hasRole(roles, "worker") && !r.hasRole(roles, "etcd") && !r.hasRole(roles, "master") {
			return true
		}
	}
	return false
}

// createRKE2Config 创建RKE2配置文件
func (r *RKE2Installer) createRKE2Config(host config.Host, nodeType string, isFirstServer bool) error {
	if r.logger != nil {
		r.logger.Info("主机 %s: 创建RKE2配置文件 (类型: %s, 角色: %v)", host.IP, nodeType, host.Role)
	}

	var config string
	serverURL := r.getServerURL()
	roles := r.normalizeRoles(host.Role)
	nodeConfig := r.getNodeConfigSection(host)

	if nodeType == "server" {
		if isFirstServer {
			// 第一个server节点配置（必须包含etcd）
			if r.hasRole(roles, "etcd") && !r.hasRole(roles, "master") {
				// 专用etcd节点
				config = fmt.Sprintf(`# RKE2 第一个etcd节点配置
token: %s
%s
# 专用etcd节点配置
disable-apiserver: true
disable-controller-manager: true
disable-scheduler: true
`, RKE2DefaultToken, nodeConfig)
			} else {
				// master节点或master+etcd混合节点（包含所有control-plane组件和etcd）
				config = fmt.Sprintf(`# RKE2 第一个master节点配置
token: %s
%s
`, RKE2DefaultToken, nodeConfig)
			}
		} else {
			// 其他server节点配置
			if r.hasRole(roles, "etcd") && !r.hasRole(roles, "master") {
				// 专用etcd节点
				config = fmt.Sprintf(`# RKE2 etcd节点配置
server: https://%s:9345
token: %s
%s
# 专用etcd节点配置
disable-apiserver: true
disable-controller-manager: true
disable-scheduler: true
`, serverURL, RKE2DefaultToken, nodeConfig)
			} else if r.hasRole(roles, "master") && !r.hasRole(roles, "etcd") {
				// 专用control-plane节点
				config = fmt.Sprintf(`# RKE2 master节点配置
server: https://%s:9345
token: %s
%s
# 专用control-plane节点配置
disable-etcd: true
`, serverURL, RKE2DefaultToken, nodeConfig)
			} else if r.hasRole(roles, "master") && r.hasRole(roles, "etcd") {
				// 混合节点（master+etcd）
				config = fmt.Sprintf(`# RKE2 混合节点配置 (master+etcd)
server: https://%s:9345
token: %s
%s
`, serverURL, RKE2DefaultToken, nodeConfig)
			}
		}
	} else {
		// worker节点配置
		config = fmt.Sprintf(`# RKE2 worker节点配置
server: https://%s:9345
token: %s
%s
`, serverURL, RKE2DefaultToken, nodeConfig)
	}

	// 创建主配置文件
	createMainConfigCmd := fmt.Sprintf(`
		cat > %s << 'EOF'
%s
EOF
		echo "RKE2主配置文件创建完成"
	`, RKE2ConfigFile, config)

	sshCmd := r.buildSSHCommand(host, createMainConfigCmd)
	if err := sshCmd.Run(); err != nil {
		return fmt.Errorf("创建RKE2主配置文件失败: %w", err)
	}

	// 创建Rainbond定制配置
	rainbondConfig := `# Rainbond定制配置
disable:
- rke2-ingress-nginx
system-default-registry: registry.cn-hangzhou.aliyuncs.com
`

	createCustomConfigCmd := fmt.Sprintf(`
		cat > %s << 'EOF'
%s
EOF
		echo "RKE2定制配置文件创建完成"
	`, RKE2CustomConfig, rainbondConfig)

	sshCmd = r.buildSSHCommand(host, createCustomConfigCmd)
	if err := sshCmd.Run(); err != nil {
		return fmt.Errorf("创建RKE2定制配置文件失败: %w", err)
	}

	// 创建镜像仓库配置
	if err := r.createRegistryConfig(host); err != nil {
		return fmt.Errorf("创建镜像仓库配置失败: %w", err)
	}

	return nil
}

// createRegistryConfig 创建镜像仓库配置文件
func (r *RKE2Installer) createRegistryConfig(host config.Host) error {
	if r.logger != nil {
		r.logger.Info("主机 %s: 创建镜像仓库配置文件", host.IP)
	}

	registryConfig := `mirrors:
  "goodrain.me":
    endpoint:
      - "https://goodrain.me"
configs:
  "goodrain.me":
    auth:
      username: admin
      password: admin1234
    tls:
      insecure_skip_verify: true`

	registryConfigPath := "/etc/rancher/rke2/registries.yaml"

	createRegistryConfigCmd := fmt.Sprintf(`
		cat > %s << 'EOF'
%s
EOF
		echo "RKE2镜像仓库配置文件创建完成"
	`, registryConfigPath, registryConfig)

	sshCmd := r.buildSSHCommand(host, createRegistryConfigCmd)
	if err := sshCmd.Run(); err != nil {
		return fmt.Errorf("创建镜像仓库配置文件失败: %w", err)
	}

	return nil
}

// getServerURL 获取server URL（第一个主机的IP）
func (r *RKE2Installer) getServerURL() string {
	serverHosts := r.getServerHosts()
	if len(serverHosts) > 0 {
		return r.getNodeIP(serverHosts[0])
	}
	return ""
}

// getNodeName 获取节点名称，如果未指定则根据IP自动生成
func (r *RKE2Installer) getNodeName(host config.Host) string {
	if host.NodeName != "" {
		return host.NodeName
	}

	// 直接使用主IP作为节点名称
	return host.IP
}

// getNodeIP 获取节点IP（直接使用主IP）
func (r *RKE2Installer) getNodeIP(host config.Host) string {
	return host.IP
}

// getNodeInternalIP 获取节点内网IP（如果有内网IP配置则返回，否则返回主IP）
func (r *RKE2Installer) getNodeInternalIP(host config.Host) string {
	if host.InternalIP != "" {
		return host.InternalIP
	}
	return host.IP
}

// executeRKE2Install 执行RKE2安装脚本
func (r *RKE2Installer) executeRKE2Install(host config.Host, nodeType string) error {
	if r.logger != nil {
		r.logger.Info("主机 %s: 执行RKE2安装脚本", host.IP)
	}

	// 执行安装
	installCmd := fmt.Sprintf(`
		echo "=== 设置RKE2安装环境变量 ==="
		export INSTALL_RKE2_TYPE="%s"
		export INSTALL_RKE2_ARTIFACT_PATH="/tmp/rke2-artifacts"

		echo "目录: $INSTALL_RKE2_ARTIFACT_PATH"
		echo "类型: $INSTALL_RKE2_TYPE"
		
		# 检查脚本文件
		if [ ! -f /tmp/rke2-artifacts/rke2-install.sh ]; then
			echo "错误: RKE2安装脚本不存在"
			exit 1
		fi
		
		echo "=== 执行RKE2安装脚本 ==="
		/tmp/rke2-artifacts/rke2-install.sh
		
		install_result=$?
		if [ $install_result -eq 0 ]; then
			echo "RKE2安装脚本执行成功"
		else
			echo "RKE2安装脚本执行失败，退出码: $install_result"
			exit $install_result
		fi
	`, nodeType)

	sshCmd := r.buildSSHCommand(host, installCmd)
	output, err := sshCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("RKE2安装脚本执行失败: %w, 输出: %s", err, string(output))
	}

	if r.logger != nil {
		r.logger.Info("主机 %s: RKE2安装脚本执行成功", host.IP)
	}
	return nil
}

// startRKE2Service 启动RKE2服务
func (r *RKE2Installer) startRKE2Service(host config.Host, nodeType string) error {
	if r.logger != nil {
		r.logger.Info("主机 %s: 启动RKE2服务", host.IP)
	}

	serviceName := fmt.Sprintf("rke2-%s", nodeType)

	startCmd := fmt.Sprintf(`
		# 启用服务
		systemctl enable %s
		
		# 启动服务
		systemctl start --no-block %s
		
		if [ $? -eq 0 ]; then
			echo "RKE2服务启动成功"
		else
			echo "RKE2服务启动失败，检查状态："
			systemctl status %s --no-pager
			exit 1
		fi
	`, serviceName, serviceName, serviceName)

	sshCmd := r.buildSSHCommand(host, startCmd)
	output, err := sshCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("启动RKE2服务失败: %w, 输出: %s", err, string(output))
	}

	if r.logger != nil {
		r.logger.Info("主机 %s: RKE2服务启动命令执行完成", host.IP)
	}
	return nil
}

// waitForServerReady 等待server节点就绪
func (r *RKE2Installer) waitForServerReady(host config.Host) error {
	if r.logger != nil {
		r.logger.Info("主机 %s: 等待RKE2 server就绪", host.IP)
	}

	for i := 0; i < 60; i++ { // 最多等待10分钟
		checkCmd := `
			if systemctl is-active rke2-server >/dev/null 2>&1; then
				if [ -f /var/lib/rancher/rke2/server/node-token ]; then
					echo "ready"
					exit 0
				fi
			fi
			echo "not ready"
			exit 1
		`

		sshCmd := r.buildSSHCommand(host, checkCmd)
		if err := sshCmd.Run(); err == nil {
			if r.logger != nil {
				r.logger.Info("主机 %s: RKE2 server已就绪", host.IP)
			}
			return nil
		}

		time.Sleep(10 * time.Second)
	}

	return fmt.Errorf("等待RKE2 server就绪超时")
}

// waitForClusterReady 等待集群就绪
func (r *RKE2Installer) waitForClusterReady(firstServer config.Host) error {
	if r.logger != nil {
		r.logger.Info("等待Kubernetes集群就绪...")
	}

	for i := 0; i < 60; i++ { // 最多等待10分钟
		checkCmd := `
			export KUBECONFIG=/etc/rancher/rke2/rke2.yaml
			export PATH=$PATH:/var/lib/rancher/rke2/bin
			
			if kubectl get nodes >/dev/null 2>&1; then
				ready_nodes=$(kubectl get nodes | grep -c "Ready")
				total_nodes=$(kubectl get nodes --no-headers | wc -l)
				echo "就绪节点: $ready_nodes/$total_nodes"
				
				if [ "$ready_nodes" -eq "$total_nodes" ] && [ "$total_nodes" -gt 0 ]; then
					echo "集群就绪"
					exit 0
				fi
			fi
			echo "集群未就绪"
			exit 1
		`

		sshCmd := r.buildSSHCommand(firstServer, checkCmd)
		if err := sshCmd.Run(); err == nil {
			if r.logger != nil {
				r.logger.Info("Kubernetes集群已就绪")
			}
			return nil
		}

		time.Sleep(10 * time.Second)
	}

	return fmt.Errorf("等待Kubernetes集群就绪超时")
}

// checkRKE2Status 检查RKE2状态
func (r *RKE2Installer) checkRKE2Status() map[string]*RKE2Status {
	results := make(map[string]*RKE2Status)

	for _, host := range r.config.Hosts {
		roles := r.normalizeRoles(host.Role)
		// 在RKE2中，如果节点有etcd或master角色，就是server节点
		// 只有纯worker节点才是agent节点
		isServer := r.hasRole(roles, "etcd") || r.hasRole(roles, "master")
		isAgent := !isServer && r.hasRole(roles, "worker")

		status := &RKE2Status{
			IP:       host.IP,
			Role:     host.Role,
			IsServer: isServer,
			IsAgent:  isAgent,
			Status:   "未知",
		}

		// 检查RKE2是否安装（使用与checkRKE2Installed相同的逻辑）
		installed, err := r.checkRKE2Installed(host)
		if err != nil {
			if r.logger != nil {
				r.logger.Debug("主机 %s: 检查RKE2状态时出错: %v", host.IP, err)
			}
			status.Status = "检查失败"
			results[host.IP] = status
			continue
		}

		if !installed {
			status.Status = "未安装"
			results[host.IP] = status
			continue
		}

		// 检查RKE2服务状态
		var serviceName string
		if status.IsServer {
			serviceName = "rke2-server"
		} else {
			serviceName = "rke2-agent"
		}

		sshCmd := r.buildSSHCommand(host, fmt.Sprintf("systemctl is-active %s", serviceName))
		if err := sshCmd.Run(); err == nil {
			status.Running = true
			// 进一步检查Kubernetes节点是否就绪
			if r.checkKubernetesNodeReady(host) {
				status.Status = "运行中"
			} else {
				status.Status = "服务运行中但节点未就绪"
			}
		} else {
			status.Running = false
			status.Status = "已安装未运行"
		}

		results[host.IP] = status
	}

	return results
}

// checkKubernetesNodeReady 检查Kubernetes节点是否就绪
func (r *RKE2Installer) checkKubernetesNodeReady(host config.Host) bool {
	// 从第一个server节点执行kubectl命令检查节点状态
	firstServer := r.getFirstEtcdHost()
	if firstServer == nil {
		if r.logger != nil {
			r.logger.Debug("找不到第一个server节点，无法检查Kubernetes节点状态")
		}
		return false
	}

	// 使用kubectl检查特定节点是否就绪
	kubectlCmd := fmt.Sprintf("kubectl get node %s -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}' 2>/dev/null || echo 'NotFound'", host.IP)
	sshCmd := r.buildSSHCommand(*firstServer, kubectlCmd)
	output, err := sshCmd.Output()
	if err != nil {
		if r.logger != nil {
			r.logger.Debug("检查节点 %s 的Kubernetes状态失败: %v", host.IP, err)
		}
		return false
	}

	nodeStatus := strings.TrimSpace(string(output))
	if r.logger != nil {
		r.logger.Debug("节点 %s 的Kubernetes就绪状态: %s", host.IP, nodeStatus)
	}
	
	// 如果节点状态是"True"，表示节点就绪
	return nodeStatus == "True"
}

// printRKE2Status 打印RKE2状态到文件，不干扰控制台进度
func (r *RKE2Installer) printRKE2Status(status map[string]*RKE2Status) {
	if r.logger != nil {
		r.logger.Debug("\n" + strings.Repeat("=", 80))
	}
	if r.logger != nil {
		r.logger.Debug("                        RKE2 集群状态")
	}
	if r.logger != nil {
		r.logger.Debug(strings.Repeat("=", 80))
	}

	// 统计信息
	running := 0
	serviceRunning := 0
	servers := 0
	agents := 0
	total := 0

	for i, host := range r.config.Hosts {
		if i > 0 {
			if r.logger != nil {
				r.logger.Debug("")
			}
		}

		result := status[host.IP]
		if result == nil {
			continue
		}

		total++

		// 状态图标和运行状态统计
		statusIcon := "✓"
		if result.Status == "运行中" {
			statusIcon = "✓"
			running++
		} else if result.Status == "服务运行中但节点未就绪" {
			statusIcon = "⚠"
			serviceRunning++
		} else {
			statusIcon = "✗"
		}

		if result.IsServer {
			servers++
		}
		if result.IsAgent {
			agents++
		}

		// 角色显示
		roleStr := []string{}
		if result.IsServer {
			roleStr = append(roleStr, "Server节点")
		}
		if result.IsAgent {
			roleStr = append(roleStr, "Agent节点")
		}
		role := strings.Join(roleStr, ",")
		if role == "" {
			role = "普通节点"
		}

		if r.logger != nil {
			r.logger.Debug(fmt.Sprintf("┌─ RKE2 #%d %s %s", i+1, statusIcon, result.Status))
		}
		if r.logger != nil {
			r.logger.Debug(fmt.Sprintf("│  IP地址        : %s", result.IP))
		}
		if r.logger != nil {
			r.logger.Debug(fmt.Sprintf("│  节点角色      : %s (%s)", strings.Join(result.Role, ","), role))
		}
		if r.logger != nil {
			r.logger.Debug(fmt.Sprintf("│  运行状态      : %t", result.Running))
		}
		if result.Error != "" {
			if r.logger != nil {
				r.logger.Debug(fmt.Sprintf("│  错误信息      : %s", result.Error))
			}
		}
		if r.logger != nil {
			r.logger.Debug("└" + strings.Repeat("─", 50))
		}
	}

	if r.logger != nil {
		r.logger.Debug("\n" + strings.Repeat("=", 80))
	}
	// 构建状态总结信息
	var statusSummary []string
	if running > 0 {
		statusSummary = append(statusSummary, fmt.Sprintf("%d个节点就绪", running))
	}
	if serviceRunning > 0 {
		statusSummary = append(statusSummary, fmt.Sprintf("%d个节点服务运行中但未就绪", serviceRunning))
	}
	failed := total - running - serviceRunning
	if failed > 0 {
		statusSummary = append(statusSummary, fmt.Sprintf("%d个节点有问题", failed))
	}
	
	statusText := strings.Join(statusSummary, ", ")
	if statusText == "" {
		statusText = "无节点状态信息"
	}
	
	if r.logger != nil {
		r.logger.Debug(fmt.Sprintf("集群总结: %s（共%d个节点: %d个Server, %d个Agent）",
			statusText, total, servers, agents))
	}
	if r.logger != nil {
		r.logger.Debug(strings.Repeat("=", 80))
	}
	if r.logger != nil {
		r.logger.Debug("")
	}
}

// 构建命令的辅助方法
func (r *RKE2Installer) buildSSHCommand(host config.Host, command string) *exec.Cmd {
	var sshCmd *exec.Cmd

	if host.Password != "" {
		if _, err := exec.LookPath("sshpass"); err != nil {
			if r.logger != nil {
				r.logger.Warn("未找到sshpass工具，请安装sshpass或为主机 %s 使用SSH密钥认证", host.IP)
			}
			sshCmd = exec.Command("ssh",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-o", "BatchMode=no",
				fmt.Sprintf("%s@%s", host.User, host.IP),
				command)
		} else {
			sshCmd = exec.Command("sshpass", "-p", host.Password, "ssh",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				fmt.Sprintf("%s@%s", host.User, host.IP),
				command)
		}
	} else if host.SSHKey != "" {
		sshCmd = exec.Command("ssh",
			"-i", host.SSHKey,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			fmt.Sprintf("%s@%s", host.User, host.IP),
			command)
	} else {
		sshCmd = exec.Command("ssh",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			fmt.Sprintf("%s@%s", host.User, host.IP),
			command)
	}

	return sshCmd
}

func (r *RKE2Installer) buildScpCommand(host config.Host, source, dest string) *exec.Cmd {
	var scpCmd *exec.Cmd
	target := fmt.Sprintf("%s@%s:%s", host.User, host.IP, dest)

	if host.Password != "" {
		if _, err := exec.LookPath("sshpass"); err == nil {
			scpCmd = exec.Command("sshpass", "-p", host.Password, "scp",
				"-C", // 启用压缩
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				source, target)
		} else {
			scpCmd = exec.Command("scp",
				"-C", // 启用压缩
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				source, target)
		}
	} else if host.SSHKey != "" {
		scpCmd = exec.Command("scp",
			"-C", // 启用压缩
			"-i", host.SSHKey,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			source, target)
	} else {
		scpCmd = exec.Command("scp",
			"-C", // 启用压缩
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			source, target)
	}

	return scpCmd
}

// configureKubectl 配置第一个server节点的kubectl
func (r *RKE2Installer) configureKubectl(host config.Host) error {
	if r.logger != nil {
		r.logger.Info("主机 %s: 配置kubectl访问", host.IP)
	}

	kubectlCmd := `
		# 创建.kube目录
		mkdir -p /root/.kube

		# 等待rke2.yaml文件生成（最多等待2分钟）
		timeout=120
		while [ $timeout -gt 0 ]; do
			if [ -f /etc/rancher/rke2/rke2.yaml ]; then
				echo "发现rke2.yaml配置文件"
				break
			fi
			echo "等待rke2.yaml文件生成... ($timeout秒)"
			sleep 5
			timeout=$((timeout - 5))
		done

		if [ ! -f /etc/rancher/rke2/rke2.yaml ]; then
			echo "错误: rke2.yaml文件未生成"
			exit 1
		fi

		# 复制kubeconfig文件
		cp /etc/rancher/rke2/rke2.yaml /root/.kube/config
		chmod 600 /root/.kube/config
		echo "kubeconfig文件已复制到 /root/.kube/config"

		# 等待kubectl文件生成并复制到系统路径
		echo "等待kubectl二进制文件生成..."
		kubectl_timeout=180
		while [ $kubectl_timeout -gt 0 ]; do
			if [ -f /var/lib/rancher/rke2/bin/kubectl ]; then
				echo "kubectl文件已生成，开始复制..."
				cp /var/lib/rancher/rke2/bin/kubectl /usr/local/bin/kubectl
				chmod +x /usr/local/bin/kubectl
				
				# 创建符号链接到 /usr/bin (兼容性)
				ln -sf /usr/local/bin/kubectl /usr/bin/kubectl
				
				echo "kubectl已安装到 /usr/local/bin/kubectl"
				break
			else
				echo "等待kubectl文件生成... (剩余 $kubectl_timeout 秒)"
				sleep 5
				kubectl_timeout=$((kubectl_timeout - 5))
			fi
		done
		
		if [ $kubectl_timeout -le 0 ]; then
			echo "警告: kubectl二进制文件在3分钟内未生成"
		fi

		# 验证kubectl配置
		export KUBECONFIG=/root/.kube/config
		echo "kubectl配置完成"
	`

	sshCmd := r.buildSSHCommand(host, kubectlCmd)

	// 注释掉实时输出，因为Logger接口没有Writer方法
	// sshCmd.Stdout = r.logger.Writer()
	// sshCmd.Stderr = r.logger.Writer()

	if r.logger != nil {
		r.logger.Info("主机 %s: 开始配置kubectl...", host.IP)
	}

	if err := sshCmd.Run(); err != nil {
		return fmt.Errorf("配置kubectl失败: %w", err)
	}

	if r.logger != nil {
		r.logger.Info("主机 %s: kubectl配置完成", host.IP)
	}
	return nil
}

// waitForNodeReady 等待节点变为Ready状态
func (r *RKE2Installer) waitForNodeReady(host config.Host) error {
	if r.logger != nil {
		r.logger.Info("主机 %s: 等待节点就绪", host.IP)
	}

	waitCmd := `
		export KUBECONFIG=/root/.kube/config
		
		# 等待节点变为Ready状态（最多等待5分钟）
		timeout=300
		echo "检查节点就绪状态..."
		
		while [ $timeout -gt 0 ]; do
			# 检查kubectl命令是否存在
			if [ ! -f /usr/local/bin/kubectl ]; then
				echo "等待kubectl工具可用... (剩余 $timeout 秒)"
				sleep 10
				timeout=$((timeout - 10))
				continue
			fi
			
			# 检查是否有Ready节点
			ready_count=$(/usr/local/bin/kubectl get nodes --no-headers 2>/dev/null | grep -c " Ready " 2>/dev/null || echo "0")
			
			# 确保ready_count是一个有效数字
			case "$ready_count" in
				''|*[!0-9]*) ready_count=0 ;;
			esac
			
			if [ "$ready_count" -gt 0 ]; then
				echo "发现 $ready_count 个Ready节点!"
				echo "当前集群状态:"
				/usr/local/bin/kubectl get nodes
				echo "节点就绪检查完成"
				break
			else
				echo "等待节点变为Ready状态... (剩余 $timeout 秒)"
				/usr/local/bin/kubectl get nodes --no-headers 2>/dev/null || echo "暂时无法获取节点信息"
				sleep 10
				timeout=$((timeout - 10))
			fi
		done
		
		if [ $timeout -le 0 ]; then
			echo "警告: 节点在5分钟内未完全就绪，但这可能是正常的"
			echo "当前节点状态:"
			/usr/local/bin/kubectl get nodes 2>/dev/null || echo "无法连接到API server"
			echo "继续安装流程..."
		fi
	`

	sshCmd := r.buildSSHCommand(host, waitCmd)

	// 注释掉实时输出，因为Logger接口没有Writer方法
	// sshCmd.Stdout = r.logger.Writer()
	// sshCmd.Stderr = r.logger.Writer()

	if r.logger != nil {
		r.logger.Info("主机 %s: 开始节点状态检查...", host.IP)
	}

	if err := sshCmd.Run(); err != nil {
		return fmt.Errorf("等待节点就绪失败: %w", err)
	}

	if r.logger != nil {
		r.logger.Info("主机 %s: 节点就绪检查完成", host.IP)
	}
	return nil
}

// checkRKE2Installed 检查RKE2是否已经安装
func (r *RKE2Installer) checkRKE2Installed(host config.Host) (bool, error) {
	if r.logger != nil {
		r.logger.Debug("主机 %s: 检查RKE2安装状态", host.IP)
	}

	checkCmd := `
		# 检查RKE2是否完整安装的严格标准
		echo "=== RKE2安装状态检查 ==="
		
		# 1. 检查systemd服务文件是否存在
		server_service=false
		agent_service=false
		if [ -f /etc/systemd/system/rke2-server.service ] || [ -f /usr/lib/systemd/system/rke2-server.service ]; then
			echo "rke2-server服务文件: 存在"
			server_service=true
		fi
		
		if [ -f /etc/systemd/system/rke2-agent.service ] || [ -f /usr/lib/systemd/system/rke2-agent.service ]; then
			echo "rke2-agent服务文件: 存在"  
			agent_service=true
		fi
		
		# 2. 检查RKE2二进制文件
		binary_exists=false
		if [ -f /usr/local/bin/rke2 ] || [ -f /var/lib/rancher/rke2/bin/rke2 ]; then
			echo "RKE2二进制文件: 存在"
			binary_exists=true
		fi
		
		# 3. 检查RKE2目录结构
		dirs_exist=false
		if [ -d /var/lib/rancher/rke2 ] && [ -d /etc/rancher/rke2 ]; then
			echo "RKE2目录结构: 存在"
			dirs_exist=true
		fi
		
		# 4. 检查是否有正在运行的服务（可选，作为额外指标）
		services_running=false
		if systemctl is-active rke2-server >/dev/null 2>&1 || systemctl is-active rke2-agent >/dev/null 2>&1; then
			echo "RKE2服务: 运行中"
			services_running=true
		fi
		
		# 安装判断：优先考虑服务运行状态，其次考虑文件完整性
		if [ "$services_running" = "true" ]; then
			echo "结果: RKE2已安装且正在运行"
			exit 0
		elif [ "$binary_exists" = "true" ] && [ "$dirs_exist" = "true" ] && ([ "$server_service" = "true" ] || [ "$agent_service" = "true" ]); then
			echo "结果: RKE2已完整安装但服务未运行"
			exit 0
		else
			echo "结果: RKE2未完整安装（可能存在残留文件）"
			echo "详细检查:"
			echo "  - 二进制文件: $binary_exists"
			echo "  - 目录结构: $dirs_exist" 
			echo "  - Server服务: $server_service"
			echo "  - Agent服务: $agent_service"
			echo "  - 服务运行: $services_running"
			exit 1
		fi
	`

	sshCmd := r.buildSSHCommand(host, checkCmd)
	output, err := sshCmd.CombinedOutput()

	// 显示检查输出到文件，不输出到控制台
	if len(output) > 0 {
		if r.logger != nil {
			r.logger.Debug("主机 %s RKE2状态检查:\n%s", host.IP, string(output))
		}
	}

	if err != nil {
		// 退出码为1表示未安装，这是正常情况
		if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
			return false, nil
		}
		// 其他错误
		return false, fmt.Errorf("检查RKE2状态失败: %w", err)
	}

	// 命令成功执行且退出码为0，表示已安装
	return true, nil
}

// transferOfflineResourcesToAllNodes 顺序传输离线资源到所有节点
func (r *RKE2Installer) transferOfflineResourcesToAllNodes() error {
	if r.logger != nil {
		r.logger.Info("开始传输离线资源到 %d 个节点", len(r.config.Hosts))
	}

	// 顺序处理每个节点，确保每个节点完整传输所有文件后再处理下一个
	for i, host := range r.config.Hosts {
		if r.logger != nil {
			r.logger.Info("=== 节点 %d/%d: %s ===", i+1, len(r.config.Hosts), host.IP)
			r.logger.Info("开始传输离线资源到节点: %s", host.IP)
		}

		// 1. 创建目录
		if err := r.createRKE2Directories(host); err != nil {
			return fmt.Errorf("节点 %s 创建目录失败: %w", host.IP, err)
		}

		// 2. 传输文件
		if err := r.transferRKE2Artifacts(host); err != nil {
			return fmt.Errorf("节点 %s 传输文件失败: %w", host.IP, err)
		}

		if r.logger != nil {
			r.logger.Info("节点 %s: 离线资源传输完成", host.IP)
			r.logger.Info("=== 节点 %d/%d: %s 传输完成 ===", i+1, len(r.config.Hosts), host.IP)
		}
	}

	if r.logger != nil {
		r.logger.Info("所有节点离线资源传输完成")
	}
	return nil
}

// validatePackageIntegrityOnAllNodes 验证所有节点的安装包完整性
func (r *RKE2Installer) validatePackageIntegrityOnAllNodes() error {
	if r.logger != nil {
		r.logger.Info("开始验证 %d 个节点的安装包完整性", len(r.config.Hosts))
	}

	// 定义需要验证的文件
	filesToValidate := []FileArtifact{
		{"rke2-install.sh", "/tmp/rke2-artifacts/rke2-install.sh", true},
		{"rke2.linux*.tar.gz", "/tmp/rke2-artifacts/rke2.linux*.tar.gz", true},
		{"sha256sum*.txt", "/tmp/rke2-artifacts/sha256sum*.txt", true},
		{"rke2-images-linux.tar", "/var/lib/rancher/rke2/agent/images/rke2-images.linux.tar", true},
		{"rainbond-offline-images.tar", "/var/lib/rancher/rke2/agent/images/rainbond-offline-images.tar", true},
	}

	// 获取本地文件信息
	localFileInfos := make(map[string]*FileInfo)
	for _, file := range filesToValidate {
		if file.required {
			if err := r.addLocalFileInfos(file, localFileInfos); err != nil {
				return fmt.Errorf("获取本地文件 %s 信息失败: %w", file.localPath, err)
			}
		}
	}

	// 顺序验证每个节点的安装包完整性
	for i, host := range r.config.Hosts {
		if r.logger != nil {
			r.logger.Info("=== 验证节点 %d/%d: %s ===", i+1, len(r.config.Hosts), host.IP)
			r.logger.Info("开始验证节点 %s 的安装包完整性", host.IP)
		}

		if err := r.validateFilesOnHost(host, filesToValidate, localFileInfos); err != nil {
			return fmt.Errorf("节点 %s 验证失败: %w", host.IP, err)
		}

		if r.logger != nil {
			r.logger.Info("节点 %s: 安装包完整性验证通过", host.IP)
			r.logger.Info("=== 节点 %d/%d: %s 验证完成 ===", i+1, len(r.config.Hosts), host.IP)
		}
	}

	if r.logger != nil {
		r.logger.Info("所有节点安装包完整性验证通过")
	}
	return nil
}
