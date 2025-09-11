package check

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

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

type BasicChecker struct {
	config       *config.Config
	logger       Logger
	stepProgress StepProgress
	results      map[string]*BasicCheckResult
	warnings     []string
}

type BasicCheckResult struct {
	IP        string
	Role      []string
	OS        string
	Arch      string
	Kernel    string
	CPUCores  int
	MemoryGB  int
	RootSpace string
	RootUsage string
	Status    string
}

func NewBasicChecker(cfg *config.Config) *BasicChecker {
	return NewBasicCheckerWithLogger(cfg, nil)
}

func NewBasicCheckerWithLogger(cfg *config.Config, logger Logger) *BasicChecker {
	return NewBasicCheckerWithLoggerAndProgress(cfg, logger, nil)
}

func NewBasicCheckerWithLoggerAndProgress(cfg *config.Config, logger Logger, stepProgress StepProgress) *BasicChecker {
	results := make(map[string]*BasicCheckResult)
	for _, host := range cfg.Hosts {
		results[host.IP] = &BasicCheckResult{
			IP:     host.IP,
			Role:   host.Role,
			Status: "检查中...",
			OS:     "未知",
			Arch:   "未知",
			Kernel: "未知",
		}
	}

	return &BasicChecker{
		config:       cfg,
		logger:       logger,
		stepProgress: stepProgress,
		results:      results,
		warnings:     make([]string, 0),
	}
}

func (c *BasicChecker) Run() error {
	if c.logger != nil {
		c.logger.Info("正在检查系统基础环境...")
	}

	// 对每个节点进行逐个检查
	for _, host := range c.config.Hosts {
		// 开始处理当前节点
		if c.stepProgress != nil {
			c.stepProgress.StartNodeProcessing(host.IP)
		}

		// 执行所有检查项目
		if err := c.checkSingleHost(host); err != nil {
			if c.logger != nil {
				c.logger.Error("节点 %s 检查失败: %v", host.IP, err)
			}
			return fmt.Errorf("节点 %s 检查失败: %w", host.IP, err)
		}

		// 完成当前节点的检查
		if c.stepProgress != nil {
			c.stepProgress.CompleteNodeStep(host.IP)
		}
	}

	// 更新所有成功的主机状态
	for _, host := range c.config.Hosts {
		if c.results[host.IP].Status != "失败" {
			c.results[host.IP].Status = "通过"
		}
	}

	if c.logger != nil {
		c.logger.Info("所有基础系统检查都已成功完成！")
	}
	return c.printResultsTableAndConfirm()
}

// checkSingleHost 对单个主机进行所有检查
func (c *BasicChecker) checkSingleHost(host config.Host) error {
	checks := []struct {
		name string
		fn   func(config.Host) error
	}{
		{"主机连通性", c.checkSingleHostConnectivity},
		{"SSH连接", c.checkSingleHostSSH},
		{"操作系统", c.checkSingleHostOS},
		{"系统架构", c.checkSingleHostArch},
		{"内核版本", c.checkSingleHostKernel},
		{"CPU", c.checkSingleHostCPU},
		{"内存", c.checkSingleHostMemory},
		{"根分区", c.checkSingleHostRootPartition},
	}

	for _, check := range checks {
		if c.logger != nil {
			c.logger.Debug("正在检查节点 %s 的 %s...", host.IP, check.name)
		}
		
		if err := check.fn(host); err != nil {
			if c.logger != nil {
				c.logger.Error("节点 %s 的 %s 检查失败: %v", host.IP, check.name, err)
			}
			c.results[host.IP].Status = "失败"
			return fmt.Errorf("%s 检查失败: %w", check.name, err)
		}
		
		if c.logger != nil {
			c.logger.Debug("✓ 节点 %s 的 %s 检查通过", host.IP, check.name)
		}
	}

	// 检查主机间连通性（只在所有主机检查完毕后执行）
	if err := c.checkInterHostConnectivityForHost(host); err != nil {
		if c.logger != nil {
			c.logger.Error("节点 %s 的主机间连通性检查失败: %v", host.IP, err)
		}
		c.results[host.IP].Status = "失败"
		return fmt.Errorf("主机间连通性检查失败: %w", err)
	}

	return nil
}

func (c *BasicChecker) checkOS() error {
	if c.logger != nil {
		c.logger.Info("正在检查远程主机操作系统...")
	}

	for i, host := range c.config.Hosts {
		if c.logger != nil {
			c.logger.Info("正在检查主机 %s 的操作系统...", host.IP)
		}

		// 构建 SSH 命令检查远程操作系统
		sshCmd := c.buildSSHCommand(host, "cat /etc/os-release")
		output, err := sshCmd.CombinedOutput()
		if err != nil {
			// 更明确的 SSH 错误分类
			lower := strings.ToLower(string(output))
			if strings.Contains(lower, "permission denied") ||
				strings.Contains(lower, "connection timed out") ||
				strings.Contains(lower, "no route to host") ||
				strings.Contains(lower, "connection refused") ||
				strings.Contains(lower, "could not resolve hostname") ||
				strings.Contains(lower, "port 22") ||
				strings.Contains(lower, "unable to authenticate") {
				return fmt.Errorf("host[%d] %s: SSH 连接失败（可能未配置免密、密码错误或未安装 sshpass）: %s", i, host.IP, strings.TrimSpace(string(output)))
			}
			return fmt.Errorf("host[%d] %s: failed to check OS: %w - %s", i, host.IP, err, strings.TrimSpace(string(output)))
		}

		supported := []string{"ubuntu", "centos", "rhel", "rocky", "openeuler"}
		osInfo := strings.ToLower(string(output))

		osDetected := false
		detectedOS := "Unknown"
		for _, os := range supported {
			if strings.Contains(osInfo, os) {
				if c.logger != nil {
					c.logger.Info("主机 %s: 检测到支持的操作系统 '%s'", host.IP, os)
				}
				detectedOS = os
				osDetected = true
				break
			}
		}

		if !osDetected {
			c.results[host.IP].Status = "失败"
			return fmt.Errorf("host[%d] %s: unsupported Linux distribution. Supported: %v", i, host.IP, supported)
		}

		c.results[host.IP].OS = detectedOS
	}

	return nil
}

func (c *BasicChecker) checkArch() error {
	if c.logger != nil {
		c.logger.Info("正在检查远程主机架构...")
	}

	for i, host := range c.config.Hosts {
		if c.logger != nil {
			c.logger.Info("正在检查主机 %s 的架构...", host.IP)
		}

		sshCmd := c.buildSSHCommand(host, "uname -m")
		output, err := sshCmd.Output()
		if err != nil {
			return fmt.Errorf("host[%d] %s: failed to check architecture: %w", i, host.IP, err)
		}

		arch := strings.TrimSpace(string(output))
		if arch != "x86_64" {
			c.results[host.IP].Status = "失败"
			return fmt.Errorf("host[%d] %s: unsupported architecture: %s (only x86_64 is supported)", i, host.IP, arch)
		}

		c.results[host.IP].Arch = arch
		if c.logger != nil {
			c.logger.Info("主机 %s: 架构 %s", host.IP, arch)
		}
	}

	return nil
}

func (c *BasicChecker) checkKernel() error {
	if c.logger != nil {
		c.logger.Info("正在检查远程主机内核版本...")
	}

	for i, host := range c.config.Hosts {
		if c.logger != nil {
			c.logger.Info("正在检查主机 %s 的内核版本...", host.IP)
		}

		sshCmd := c.buildSSHCommand(host, "uname -r")
		output, err := sshCmd.Output()
		if err != nil {
			c.results[host.IP].Status = "失败"
			return fmt.Errorf("host[%d] %s: failed to check kernel version: %w", i, host.IP, err)
		}

		kernel := strings.TrimSpace(string(output))
		c.results[host.IP].Kernel = kernel
		if c.logger != nil {
			c.logger.Info("主机 %s: 内核版本 %s", host.IP, kernel)
		}

		// 检查内核版本是否符合要求
		if c.checkKernelCompatibility(kernel) {
			if c.logger != nil {
				c.logger.Info("主机 %s: 内核版本兼容", host.IP)
			}
		} else {
			warning := fmt.Sprintf("主机 %s 内核版本过低: %s (最少需要 4.x)", host.IP, kernel)
			c.warnings = append(c.warnings, warning)
			if c.logger != nil {
				c.logger.Warn("主机 %s: 内核版本 %s 低于最低要求 (4.x)", host.IP, kernel)
			}
		}
	}

	return nil
}

func (c *BasicChecker) checkKernelCompatibility(kernel string) bool {
	// 解析内核版本，检查是否 >= 4.0
	parts := strings.Split(kernel, ".")
	if len(parts) >= 1 {
		majorVersion, err := strconv.Atoi(parts[0])
		if err == nil {
			return majorVersion >= 4
		}
	}
	return false
}

func (c *BasicChecker) checkCPU() error {
	if c.logger != nil {
		c.logger.Info("正在检查远程主机CPU...")
	}

	for i, host := range c.config.Hosts {
		if c.logger != nil {
			c.logger.Info("正在检查主机 %s 的CPU...", host.IP)
		}

		sshCmd := c.buildSSHCommand(host, "nproc")
		output, err := sshCmd.Output()
		if err != nil {
			return fmt.Errorf("host[%d] %s: failed to check CPU: %w", i, host.IP, err)
		}

		cpuCount, err := strconv.Atoi(strings.TrimSpace(string(output)))
		if err != nil {
			return fmt.Errorf("host[%d] %s: failed to parse CPU count: %w", i, host.IP, err)
		}

		if cpuCount < 2 {
			c.results[host.IP].Status = "失败"
			return fmt.Errorf("host[%d] %s: insufficient CPU cores: %d (minimum 2 required)", i, host.IP, cpuCount)
		}

		c.results[host.IP].CPUCores = cpuCount
		if c.logger != nil {
			c.logger.Info("主机 %s: CPU核心数: %d", host.IP, cpuCount)
		}
	}

	return nil
}

func (c *BasicChecker) checkMemory() error {
	if c.logger != nil {
		c.logger.Info("正在检查远程主机内存...")
	}

	for i, host := range c.config.Hosts {
		if c.logger != nil {
			c.logger.Info("正在检查主机 %s 的内存...", host.IP)
		}

		sshCmd := c.buildSSHCommand(host, "free -m | grep '^Mem:' | awk '{print $2}'")
		output, err := sshCmd.Output()
		if err != nil {
			return fmt.Errorf("host[%d] %s: failed to check memory: %w", i, host.IP, err)
		}

		memMB, err := strconv.Atoi(strings.TrimSpace(string(output)))
		if err != nil {
			return fmt.Errorf("host[%d] %s: failed to parse memory size: %w", i, host.IP, err)
		}

		memGB := memMB / 1024
		c.results[host.IP].MemoryGB = memGB

		if memGB < 4 {
			warning := fmt.Sprintf("主机 %s 内存不足: %d GB (最少需要 4 GB)", host.IP, memGB)
			c.warnings = append(c.warnings, warning)
			if c.logger != nil {
				c.logger.Warn("主机 %s 内存不足: %dGB (建议最少4GB)", host.IP, memGB)
			}
		} else {
			if c.logger != nil {
				c.logger.Info("主机 %s: 内存: %dGB", host.IP, memGB)
			}
		}
	}

	return nil
}

func (c *BasicChecker) checkRootPartition() error {
	if c.logger != nil {
		c.logger.Info("正在检查根分区空间...")
	}
	for i, host := range c.config.Hosts {
		if c.logger != nil {
			c.logger.Info("正在检查主机 %s 的根分区...", host.IP)
		}
		sshCmd := c.buildSSHCommand(host, "df -BG / | tail -1")
		output, err := sshCmd.Output()
		if err != nil {
			c.results[host.IP].Status = "失败"
			return fmt.Errorf("host[%d] %s: failed to check root partition: %w", i, host.IP, err)
		}
		fields := strings.Fields(strings.TrimSpace(string(output)))
		if len(fields) >= 5 {
			totalSpaceStr := fields[1]
			usage := fields[4]
			availSpaceStr := fields[3]

			// 解析可用空间大小 (去掉G后缀)
			availSizeStr := strings.TrimSuffix(availSpaceStr, "G")
			availSpaceGB, err := strconv.Atoi(availSizeStr)
			if err == nil && availSpaceGB < 50 {
				warning := fmt.Sprintf("主机 %s 根分区可用空间不足: %d GB (最少需要 50 GB)", host.IP, availSpaceGB)
				c.warnings = append(c.warnings, warning)
				if c.logger != nil {
					c.logger.Warn("主机 %s 根分区空间不足: %dGB 可用 (建议最少50GB)", host.IP, availSpaceGB)
				}
			}

			c.results[host.IP].RootSpace = fmt.Sprintf("%s/%s", availSpaceStr, totalSpaceStr)
			c.results[host.IP].RootUsage = usage
		} else {
			c.results[host.IP].RootSpace = "未知"
			c.results[host.IP].RootUsage = "未知"
		}
	}
	return nil
}

func (c *BasicChecker) checkHosts() error {
	for i, host := range c.config.Hosts {
		if c.logger != nil {
			c.logger.Info("检查主机 %s (%s)...", host.IP, strings.Join(host.Role, ","))
		}

		cmd := exec.Command("ping", "-c", "1", "-W", "3", host.IP)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("host[%d] %s is not reachable: %w", i, host.IP, err)
		}
	}

	return nil
}

// checkSSHConnectivity 检查 SSH 连通性与认证能力
func (c *BasicChecker) checkSSHConnectivity() error {
	for i, host := range c.config.Hosts {
		if c.logger != nil {
			c.logger.Info("正在检查主机 %s 的SSH连接...", host.IP)
		}
		sshCmd := c.buildSSHCommand(host, "echo ok")
		output, err := sshCmd.CombinedOutput()
		if err != nil {
			lower := strings.ToLower(string(output))
			// 将典型的 SSH 失败归类为“无法连接/未配置免密/认证失败”
			if strings.Contains(lower, "permission denied") ||
				strings.Contains(lower, "connection timed out") ||
				strings.Contains(lower, "no route to host") ||
				strings.Contains(lower, "connection refused") ||
				strings.Contains(lower, "could not resolve hostname") ||
				strings.Contains(lower, "port 22") ||
				strings.Contains(lower, "unable to authenticate") {
				c.results[host.IP].Status = "失败"
				return fmt.Errorf("host[%d] %s: SSH 连接失败（可能未配置免密、密码错误或未安装 sshpass）: %s", i, host.IP, strings.TrimSpace(string(output)))
			}
			c.results[host.IP].Status = "失败"
			return fmt.Errorf("host[%d] %s: SSH 连接失败: %s", i, host.IP, strings.TrimSpace(string(output)))
		}
		// 放宽判断：只要输出包含 ok（忽略大小写与前后提示），认为 SSH 可用
		if !strings.Contains(strings.ToLower(string(output)), "ok") {
			if c.logger != nil {
				c.logger.Warn("主机 %s: 意外的SSH探测输出: %s", host.IP, strings.TrimSpace(string(output)))
			}
		}
	}
	return nil
}

// checkInterHostConnectivity 检查主机间连通性
func (c *BasicChecker) checkInterHostConnectivity() error {
	if c.logger != nil {
		c.logger.Info("检查主机间连通性...")
	}

	if len(c.config.Hosts) < 2 {
		if c.logger != nil {
			c.logger.Info("仅配置一台主机，跳过主机间连接检查")
		}
		return nil
	}

	for i, sourceHost := range c.config.Hosts {
		for j, targetHost := range c.config.Hosts {
			if i == j {
				continue // 跳过自己
			}

			if c.logger != nil {
				c.logger.Info("测试主机的连通性 %s 到 %s...", sourceHost.IP, targetHost.IP)
			}

			// 使用SSH在源主机上执行ping命令到目标主机
			pingCmd := fmt.Sprintf("ping -c 4 -W 3 %s", targetHost.IP)
			sshCmd := c.buildSSHCommand(sourceHost, pingCmd)
			output, err := sshCmd.CombinedOutput()

			if err != nil {
				c.results[sourceHost.IP].Status = "失败"
				return fmt.Errorf("host[%d] %s: failed to ping host %s: %w - %s",
					i, sourceHost.IP, targetHost.IP, err, strings.TrimSpace(string(output)))
			}

			// 检查是否有丢包
			outputStr := string(output)
			if strings.Contains(outputStr, "100% packet loss") {
				c.results[sourceHost.IP].Status = "失败"
				return fmt.Errorf("host[%d] %s: 100%% packet loss to host %s",
					i, sourceHost.IP, targetHost.IP)
			}

			// 检查是否有任何丢包
			if strings.Contains(outputStr, "packet loss") {
				// 提取丢包率信息进行更详细的检查
				lines := strings.Split(outputStr, "\n")
				for _, line := range lines {
					if strings.Contains(line, "packet loss") && !strings.Contains(line, "0% packet loss") && !strings.Contains(line, "0.0% packet loss") {
						warning := fmt.Sprintf("主机 %s 到 %s 有丢包: %s", sourceHost.IP, targetHost.IP, strings.TrimSpace(line))
						c.warnings = append(c.warnings, warning)
						if c.logger != nil {
							c.logger.Warn("检测到从 %s 到 %s 的丢包: %s", sourceHost.IP, targetHost.IP, strings.TrimSpace(line))
						}
						break
					}
				}
			}

			if c.logger != nil {
				c.logger.Info("✓ 主机 %s 可以达到 %s", sourceHost.IP, targetHost.IP)
			}
		}
	}

	return nil
}

// buildSSHCommand 构建 SSH 命令
func (c *BasicChecker) buildSSHCommand(host config.Host, command string) *exec.Cmd {
	var sshCmd *exec.Cmd

	if host.Password != "" {
		// 检查 sshpass 是否可用
		if _, err := exec.LookPath("sshpass"); err != nil {
			if c.logger != nil {
				c.logger.Warn("未找到 sshpass。请为主机 %s 安装 sshpass 或使用 SSH 密钥认证", host.IP)
			}
			if c.logger != nil {
				c.logger.Warn("安装 sshpass: 'brew install hudochenkov/sshpass/sshpass' (macOS) 或 'apt-get install sshpass' (Ubuntu)")
			}
			// 尝试使用 expect 或提示用户
			sshCmd = exec.Command("ssh",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-o", "BatchMode=yes",
				"-o", "LogLevel=ERROR",
				"-o", "ConnectTimeout=5",
				fmt.Sprintf("%s@%s", host.User, host.IP),
				command)
		} else {
			// 使用密码登录 (需要 sshpass)
			sshCmd = exec.Command("sshpass", "-p", host.Password, "ssh",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-o", "LogLevel=ERROR",
				"-o", "ConnectTimeout=5",
				fmt.Sprintf("%s@%s", host.User, host.IP),
				command)
		}
	} else if host.SSHKey != "" {
		// 使用 SSH 密钥登录
		sshCmd = exec.Command("ssh",
			"-i", host.SSHKey,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "BatchMode=yes",
			"-o", "LogLevel=ERROR",
			"-o", "ConnectTimeout=5",
			fmt.Sprintf("%s@%s", host.User, host.IP),
			command)
	} else {
		// 默认使用系统 SSH 配置
		sshCmd = exec.Command("ssh",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "BatchMode=yes",
			"-o", "LogLevel=ERROR",
			"-o", "ConnectTimeout=5",
			fmt.Sprintf("%s@%s", host.User, host.IP),
			command)
	}

	return sshCmd
}

// printResultsTableAndConfirm 打印基础检测结果表格并确认是否继续
func (c *BasicChecker) printResultsTableAndConfirm() error {
	if c.logger != nil {
		c.logger.Info("\n" + strings.Repeat("=", 80))
		c.logger.Info("                    基础系统检查结果")
		c.logger.Info(strings.Repeat("=", 80))
	}

	// 统计信息
	passed := 0
	failed := 0
	for _, result := range c.results {
		if result.Status == "通过" {
			passed++
		} else if result.Status == "失败" {
			failed++
		}
	}

	// 为每个主机打印一个纵向的信息块
	for i, host := range c.config.Hosts {
		if i > 0 {
			if c.logger != nil {
				c.logger.Info("") // 主机间空行
			}
		}

		result := c.results[host.IP]

		// 格式化显示
		memStr := "未知"
		if result.MemoryGB > 0 {
			memStr = fmt.Sprintf("%d GB", result.MemoryGB)
		}

		cpuStr := "未知"
		if result.CPUCores > 0 {
			cpuStr = fmt.Sprintf("%d 核", result.CPUCores)
		}

		// 直接使用配置文件中的角色
		roleStr := strings.Join(result.Role, ",")

		// 格式化分区信息
		rootInfo := "未知"
		if result.RootSpace != "" && result.RootSpace != "未知" && result.RootSpace != "Unknown" {
			if result.RootUsage != "" && result.RootUsage != "未知" && result.RootUsage != "Unknown" {
				rootInfo = fmt.Sprintf("%s (使用率: %s)", result.RootSpace, result.RootUsage)
			} else {
				rootInfo = result.RootSpace
			}
		}

		// 状态显示
		statusStr := result.Status
		statusIcon := "✓"
		if result.Status == "失败" {
			statusIcon = "✗"
		} else if result.Status == "检查中..." {
			statusIcon = "⏳"
		}

		// 打印主机信息块
		if c.logger != nil {
			c.logger.Info("┌─ 主机 #%d %s %s", i+1, statusIcon, statusStr)
			c.logger.Info("│  IP地址      : %s", result.IP)
			c.logger.Info("│  角色        : %s", roleStr)
			c.logger.Info("│  操作系统    : %s", result.OS)
			c.logger.Info("│  架构        : %s", result.Arch)
			c.logger.Info("│  内核版本    : %s", result.Kernel)
			c.logger.Info("│  CPU         : %s", cpuStr)
			c.logger.Info("│  内存        : %s", memStr)
			c.logger.Info("│  根分区      : %s", rootInfo)
			c.logger.Info("└" + strings.Repeat("─", 50))
		}
	}

	if c.logger != nil {
		c.logger.Info("\n" + strings.Repeat("=", 80))
		c.logger.Info("检查总结: %d 个主机通过检查, %d 个主机检查失败", passed, failed)
		c.logger.Info(strings.Repeat("=", 80))
	}

	// 显示警告信息并询问用户是否继续
	if len(c.warnings) > 0 {
		fmt.Printf("\n⚠️  发现以下问题:\n")
		for i, warning := range c.warnings {
			fmt.Printf("  %d. %s\n", i+1, warning)
		}
		fmt.Printf("\n这些问题可能导致安装失败或运行不稳定。\n")
		fmt.Printf("是否继续安装? (y/N): ")

		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("无法读取用户输入: %w", err)
		}

		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			return fmt.Errorf("用户取消安装")
		}

		fmt.Printf("继续安装...\n")
	}

	fmt.Println()
	return nil
}

// checkSingleHostConnectivity 检查单个主机连通性
func (c *BasicChecker) checkSingleHostConnectivity(host config.Host) error {
	if c.logger != nil {
		c.logger.Debug("检查主机 %s (%s)...", host.IP, strings.Join(host.Role, ","))
	}

	cmd := exec.Command("ping", "-c", "1", "-W", "3", host.IP)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("主机 %s 无法连通: %w", host.IP, err)
	}

	return nil
}

// checkSingleHostSSH 检查单个主机SSH连接
func (c *BasicChecker) checkSingleHostSSH(host config.Host) error {
	if c.logger != nil {
		c.logger.Debug("正在检查主机 %s 的SSH连接...", host.IP)
	}
	sshCmd := c.buildSSHCommand(host, "echo ok")
	output, err := sshCmd.CombinedOutput()
	if err != nil {
		lower := strings.ToLower(string(output))
		if strings.Contains(lower, "permission denied") ||
			strings.Contains(lower, "connection timed out") ||
			strings.Contains(lower, "no route to host") ||
			strings.Contains(lower, "connection refused") ||
			strings.Contains(lower, "could not resolve hostname") ||
			strings.Contains(lower, "port 22") ||
			strings.Contains(lower, "unable to authenticate") {
			c.results[host.IP].Status = "失败"
			return fmt.Errorf("SSH 连接失败（可能未配置免密、密码错误或未安装 sshpass）: %s", strings.TrimSpace(string(output)))
		}
		c.results[host.IP].Status = "失败"
		return fmt.Errorf("SSH 连接失败: %s", strings.TrimSpace(string(output)))
	}
	if !strings.Contains(strings.ToLower(string(output)), "ok") {
		if c.logger != nil {
			c.logger.Warn("主机 %s: 意外的SSH探测输出: %s", host.IP, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

// checkSingleHostOS 检查单个主机操作系统
func (c *BasicChecker) checkSingleHostOS(host config.Host) error {
	if c.logger != nil {
		c.logger.Debug("正在检查主机 %s 的操作系统...", host.IP)
	}

	sshCmd := c.buildSSHCommand(host, "cat /etc/os-release")
	output, err := sshCmd.CombinedOutput()
	if err != nil {
		lower := strings.ToLower(string(output))
		if strings.Contains(lower, "permission denied") ||
			strings.Contains(lower, "connection timed out") ||
			strings.Contains(lower, "no route to host") ||
			strings.Contains(lower, "connection refused") ||
			strings.Contains(lower, "could not resolve hostname") ||
			strings.Contains(lower, "port 22") ||
			strings.Contains(lower, "unable to authenticate") {
			return fmt.Errorf("SSH 连接失败（可能未配置免密、密码错误或未安装 sshpass）: %s", strings.TrimSpace(string(output)))
		}
		return fmt.Errorf("检查操作系统失败: %w - %s", err, strings.TrimSpace(string(output)))
	}

	supported := []string{"ubuntu", "centos", "rhel", "rocky", "openeuler"}
	osInfo := strings.ToLower(string(output))

	osDetected := false
	detectedOS := "Unknown"
	for _, os := range supported {
		if strings.Contains(osInfo, os) {
			if c.logger != nil {
				c.logger.Debug("主机 %s: 检测到支持的操作系统 '%s'", host.IP, os)
			}
			detectedOS = os
			osDetected = true
			break
		}
	}

	if !osDetected {
		c.results[host.IP].Status = "失败"
		return fmt.Errorf("不支持的Linux发行版。支持的版本: %v", supported)
	}

	c.results[host.IP].OS = detectedOS
	return nil
}

// checkSingleHostArch 检查单个主机架构
func (c *BasicChecker) checkSingleHostArch(host config.Host) error {
	if c.logger != nil {
		c.logger.Debug("正在检查主机 %s 的架构...", host.IP)
	}

	sshCmd := c.buildSSHCommand(host, "uname -m")
	output, err := sshCmd.Output()
	if err != nil {
		return fmt.Errorf("检查架构失败: %w", err)
	}

	arch := strings.TrimSpace(string(output))
	if arch != "x86_64" {
		c.results[host.IP].Status = "失败"
		return fmt.Errorf("不支持的架构: %s (仅支持 x86_64)", arch)
	}

	c.results[host.IP].Arch = arch
	if c.logger != nil {
		c.logger.Debug("主机 %s: 架构 %s", host.IP, arch)
	}
	return nil
}

// checkSingleHostKernel 检查单个主机内核版本
func (c *BasicChecker) checkSingleHostKernel(host config.Host) error {
	if c.logger != nil {
		c.logger.Debug("正在检查主机 %s 的内核版本...", host.IP)
	}

	sshCmd := c.buildSSHCommand(host, "uname -r")
	output, err := sshCmd.Output()
	if err != nil {
		c.results[host.IP].Status = "失败"
		return fmt.Errorf("检查内核版本失败: %w", err)
	}

	kernel := strings.TrimSpace(string(output))
	c.results[host.IP].Kernel = kernel
	if c.logger != nil {
		c.logger.Debug("主机 %s: 内核版本 %s", host.IP, kernel)
	}

	if c.checkKernelCompatibility(kernel) {
		if c.logger != nil {
			c.logger.Debug("主机 %s: 内核版本兼容", host.IP)
		}
	} else {
		warning := fmt.Sprintf("主机 %s 内核版本过低: %s (最少需要 4.x)", host.IP, kernel)
		c.warnings = append(c.warnings, warning)
		if c.logger != nil {
			c.logger.Warn("主机 %s: 内核版本 %s 低于最低要求 (4.x)", host.IP, kernel)
		}
	}
	return nil
}

// checkSingleHostCPU 检查单个主机CPU
func (c *BasicChecker) checkSingleHostCPU(host config.Host) error {
	if c.logger != nil {
		c.logger.Debug("正在检查主机 %s 的CPU...", host.IP)
	}

	sshCmd := c.buildSSHCommand(host, "nproc")
	output, err := sshCmd.Output()
	if err != nil {
		return fmt.Errorf("检查CPU失败: %w", err)
	}

	cpuCount, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return fmt.Errorf("解析CPU数量失败: %w", err)
	}

	if cpuCount < 2 {
		c.results[host.IP].Status = "失败"
		return fmt.Errorf("CPU核心数不足: %d (最少需要 2)", cpuCount)
	}

	c.results[host.IP].CPUCores = cpuCount
	if c.logger != nil {
		c.logger.Debug("主机 %s: CPU核心数: %d", host.IP, cpuCount)
	}
	return nil
}

// checkSingleHostMemory 检查单个主机内存
func (c *BasicChecker) checkSingleHostMemory(host config.Host) error {
	if c.logger != nil {
		c.logger.Debug("正在检查主机 %s 的内存...", host.IP)
	}

	sshCmd := c.buildSSHCommand(host, "free -m | grep '^Mem:' | awk '{print $2}'")
	output, err := sshCmd.Output()
	if err != nil {
		return fmt.Errorf("检查内存失败: %w", err)
	}

	memMB, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return fmt.Errorf("解析内存大小失败: %w", err)
	}

	memGB := memMB / 1024
	c.results[host.IP].MemoryGB = memGB

	if memGB < 4 {
		warning := fmt.Sprintf("主机 %s 内存不足: %d GB (最少需要 4 GB)", host.IP, memGB)
		c.warnings = append(c.warnings, warning)
		if c.logger != nil {
			c.logger.Warn("主机 %s 内存不足: %dGB (建议最少4GB)", host.IP, memGB)
		}
	} else {
		if c.logger != nil {
			c.logger.Debug("主机 %s: 内存: %dGB", host.IP, memGB)
		}
	}
	return nil
}

// checkSingleHostRootPartition 检查单个主机根分区
func (c *BasicChecker) checkSingleHostRootPartition(host config.Host) error {
	if c.logger != nil {
		c.logger.Debug("正在检查主机 %s 的根分区...", host.IP)
	}
	sshCmd := c.buildSSHCommand(host, "df -BG / | tail -1")
	output, err := sshCmd.Output()
	if err != nil {
		c.results[host.IP].Status = "失败"
		return fmt.Errorf("检查根分区失败: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) >= 5 {
		totalSpaceStr := fields[1]
		usage := fields[4]
		availSpaceStr := fields[3]

		availSizeStr := strings.TrimSuffix(availSpaceStr, "G")
		availSpaceGB, err := strconv.Atoi(availSizeStr)
		if err == nil && availSpaceGB < 50 {
			warning := fmt.Sprintf("主机 %s 根分区可用空间不足: %d GB (最少需要 50 GB)", host.IP, availSpaceGB)
			c.warnings = append(c.warnings, warning)
			if c.logger != nil {
				c.logger.Warn("主机 %s 根分区空间不足: %dGB 可用 (建议最少50GB)", host.IP, availSpaceGB)
			}
		}

		c.results[host.IP].RootSpace = fmt.Sprintf("%s/%s", availSpaceStr, totalSpaceStr)
		c.results[host.IP].RootUsage = usage
	} else {
		c.results[host.IP].RootSpace = "未知"
		c.results[host.IP].RootUsage = "未知"
	}
	return nil
}

// checkInterHostConnectivityForHost 检查从特定主机到其他主机的连通性
func (c *BasicChecker) checkInterHostConnectivityForHost(sourceHost config.Host) error {
	if c.logger != nil {
		c.logger.Debug("检查从主机 %s 的主机间连通性...", sourceHost.IP)
	}

	if len(c.config.Hosts) < 2 {
		if c.logger != nil {
			c.logger.Debug("仅配置一台主机，跳过主机间连接检查")
		}
		return nil
	}

	for _, targetHost := range c.config.Hosts {
		if sourceHost.IP == targetHost.IP {
			continue // 跳过自己
		}

		if c.logger != nil {
			c.logger.Debug("测试主机的连通性 %s 到 %s...", sourceHost.IP, targetHost.IP)
		}

		// 使用SSH在源主机上执行ping命令到目标主机
		pingCmd := fmt.Sprintf("ping -c 4 -W 3 %s", targetHost.IP)
		sshCmd := c.buildSSHCommand(sourceHost, pingCmd)
		output, err := sshCmd.CombinedOutput()

		if err != nil {
			c.results[sourceHost.IP].Status = "失败"
			return fmt.Errorf("无法ping主机 %s: %w - %s", targetHost.IP, err, strings.TrimSpace(string(output)))
		}

		// 检查是否有丢包
		outputStr := string(output)
		if strings.Contains(outputStr, "100% packet loss") {
			c.results[sourceHost.IP].Status = "失败"
			return fmt.Errorf("与主机 %s 100%% 丢包", targetHost.IP)
		}

		// 检查是否有任何丢包
		if strings.Contains(outputStr, "packet loss") {
			lines := strings.Split(outputStr, "\n")
			for _, line := range lines {
				if strings.Contains(line, "packet loss") && !strings.Contains(line, "0% packet loss") && !strings.Contains(line, "0.0% packet loss") {
					warning := fmt.Sprintf("主机 %s 到 %s 有丢包: %s", sourceHost.IP, targetHost.IP, strings.TrimSpace(line))
					c.warnings = append(c.warnings, warning)
					if c.logger != nil {
						c.logger.Warn("检测到从 %s 到 %s 的丢包: %s", sourceHost.IP, targetHost.IP, strings.TrimSpace(line))
					}
					break
				}
			}
		}

		if c.logger != nil {
			c.logger.Debug("✓ 主机 %s 可以达到 %s", sourceHost.IP, targetHost.IP)
		}
	}

	return nil
}
