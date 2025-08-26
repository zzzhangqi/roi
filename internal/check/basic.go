package check

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/rainbond/rainbond-offline-installer/pkg/config"
	"github.com/sirupsen/logrus"
)

type BasicChecker struct {
	config   *config.Config
	logger   *logrus.Logger
	results  map[string]*BasicCheckResult
	warnings []string
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
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)

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
		config:   cfg,
		logger:   logger,
		results:  results,
		warnings: make([]string, 0),
	}
}

func (c *BasicChecker) Run() error {
	c.logger.Info("Starting basic system environment check...")

	checks := []struct {
		name string
		fn   func() error
	}{
		{"Host Connectivity", c.checkHosts},
		{"SSH Connectivity", c.checkSSHConnectivity},
		{"Inter-Host Connectivity", c.checkInterHostConnectivity},
		{"Operating System", c.checkOS},
		{"Architecture", c.checkArch},
		{"Kernel Version", c.checkKernel},
		{"CPU", c.checkCPU},
		{"Memory", c.checkMemory},
		{"Root Partition", c.checkRootPartition},
	}

	for _, check := range checks {
		c.logger.Infof("Checking %s...", check.name)
		if err := check.fn(); err != nil {
			c.logger.Errorf("Check failed for %s: %v", check.name, err)
			return fmt.Errorf("check failed for %s: %w", check.name, err)
		}
		c.logger.Infof("✓ %s check passed", check.name)
	}

	// 更新所有成功的主机状态
	for _, host := range c.config.Hosts {
		if c.results[host.IP].Status != "失败" {
			c.results[host.IP].Status = "通过"
		}
	}

	c.logger.Info("All basic system checks passed successfully!")
	return c.printResultsTableAndConfirm()
}

func (c *BasicChecker) checkOS() error {
	c.logger.Info("Checking remote hosts operating systems...")

	for i, host := range c.config.Hosts {
		c.logger.Infof("Checking OS for host %s...", host.IP)

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
				c.logger.Infof("Host %s: detected supported OS containing '%s'", host.IP, os)
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
	c.logger.Info("Checking remote hosts architecture...")

	for i, host := range c.config.Hosts {
		c.logger.Infof("Checking architecture for host %s...", host.IP)

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
		c.logger.Infof("Host %s: architecture %s", host.IP, arch)
	}

	return nil
}

func (c *BasicChecker) checkKernel() error {
	c.logger.Info("Checking remote hosts kernel version...")

	for i, host := range c.config.Hosts {
		c.logger.Infof("Checking kernel version for host %s...", host.IP)

		sshCmd := c.buildSSHCommand(host, "uname -r")
		output, err := sshCmd.Output()
		if err != nil {
			c.results[host.IP].Status = "失败"
			return fmt.Errorf("host[%d] %s: failed to check kernel version: %w", i, host.IP, err)
		}

		kernel := strings.TrimSpace(string(output))
		c.results[host.IP].Kernel = kernel
		c.logger.Infof("Host %s: kernel version %s", host.IP, kernel)

		// 检查内核版本是否符合要求
		if c.checkKernelCompatibility(kernel) {
			c.logger.Infof("Host %s: kernel version is compatible", host.IP)
		} else {
			warning := fmt.Sprintf("主机 %s 内核版本过低: %s (最少需要 4.x)", host.IP, kernel)
			c.warnings = append(c.warnings, warning)
			c.logger.Warnf("Host %s: kernel version %s is below minimum requirement (4.x)", host.IP, kernel)
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
	c.logger.Info("Checking remote hosts CPU...")

	for i, host := range c.config.Hosts {
		c.logger.Infof("Checking CPU for host %s...", host.IP)

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
		c.logger.Infof("Host %s: CPU cores: %d", host.IP, cpuCount)
	}

	return nil
}

func (c *BasicChecker) checkMemory() error {
	c.logger.Info("Checking remote hosts memory...")

	for i, host := range c.config.Hosts {
		c.logger.Infof("Checking memory for host %s...", host.IP)

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
			c.logger.Warnf("主机 %s 内存不足: %dGB (建议最少4GB)", host.IP, memGB)
		} else {
			c.logger.Infof("Host %s: Memory: %dGB", host.IP, memGB)
		}
	}

	return nil
}

func (c *BasicChecker) checkRootPartition() error {
	c.logger.Info("Checking root partition space...")
	for i, host := range c.config.Hosts {
		c.logger.Infof("Checking root partition for host %s...", host.IP)
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
				c.logger.Warnf("主机 %s 根分区空间不足: %dGB 可用 (建议最少50GB)", host.IP, availSpaceGB)
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
		c.logger.Infof("Checking host %s (%s)...", host.IP, strings.Join(host.Role, ","))

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
		c.logger.Infof("Checking SSH connectivity for host %s...", host.IP)
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
			c.logger.Warnf("Host %s: unexpected SSH probe output: %s", host.IP, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

// checkInterHostConnectivity 检查主机间连通性
func (c *BasicChecker) checkInterHostConnectivity() error {
	c.logger.Info("Checking inter-host connectivity...")
	
	if len(c.config.Hosts) < 2 {
		c.logger.Info("Only one host configured, skipping inter-host connectivity check")
		return nil
	}

	for i, sourceHost := range c.config.Hosts {
		for j, targetHost := range c.config.Hosts {
			if i == j {
				continue // 跳过自己
			}
			
			c.logger.Infof("Testing connectivity from host %s to %s...", sourceHost.IP, targetHost.IP)
			
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
						c.logger.Warnf("Packet loss detected from %s to %s: %s", sourceHost.IP, targetHost.IP, strings.TrimSpace(line))
						break
					}
				}
			}
			
			c.logger.Infof("✓ Host %s can reach %s", sourceHost.IP, targetHost.IP)
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
			c.logger.Warnf("sshpass not found. Please install sshpass or use SSH key authentication for host %s", host.IP)
			c.logger.Warnf("Install sshpass: 'brew install hudochenkov/sshpass/sshpass' (macOS) or 'apt-get install sshpass' (Ubuntu)")
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
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("                    基础系统检查结果")
	fmt.Println(strings.Repeat("=", 80))

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
			fmt.Println() // 主机间空行
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
		fmt.Printf("┌─ 主机 #%d %s %s\n", i+1, statusIcon, statusStr)
		fmt.Printf("│  IP地址      : %s\n", result.IP)
		fmt.Printf("│  角色        : %s\n", roleStr)
		fmt.Printf("│  操作系统    : %s\n", result.OS)
		fmt.Printf("│  架构        : %s\n", result.Arch)
		fmt.Printf("│  内核版本    : %s\n", result.Kernel)
		fmt.Printf("│  CPU         : %s\n", cpuStr)
		fmt.Printf("│  内存        : %s\n", memStr)
		fmt.Printf("│  根分区      : %s\n", rootInfo)
		fmt.Printf("└" + strings.Repeat("─", 50))
	}

	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Printf("检查总结: %d 个主机通过检查, %d 个主机检查失败\n", passed, failed)
	fmt.Println(strings.Repeat("=", 80))
	
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
