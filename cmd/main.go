package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rainbond/rainbond-offline-installer/internal/check"
	"github.com/rainbond/rainbond-offline-installer/internal/lvm"
	"github.com/rainbond/rainbond-offline-installer/internal/mysql"
	"github.com/rainbond/rainbond-offline-installer/internal/optimize"
	"github.com/rainbond/rainbond-offline-installer/internal/rainbond"
	"github.com/rainbond/rainbond-offline-installer/internal/rke2"
	"github.com/rainbond/rainbond-offline-installer/pkg/config"
	"github.com/rainbond/rainbond-offline-installer/pkg/logger"
	"github.com/rainbond/rainbond-offline-installer/pkg/progress"
	"github.com/rainbond/rainbond-offline-installer/pkg/ssh"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile string
	verbose bool
)

var (
	checkFlag    bool
	lvmFlag      bool
	optimizeFlag bool
	rke2Flag     bool
	mysqlFlag    bool
	rainbondFlag bool
)

var (
	sshUnifiedPassword bool
	sshForceGenerate   bool
	sshMethod          string
)

var rootCmd = &cobra.Command{
	Use:   "roi",
	Short: "Rainbond Offline Installer - A tool for deploying Rainbond clusters",
	Long: `Rainbond Offline Installer (ROI) is a CLI tool built with Go and Cobra
that provides both online and offline deployment modes for Rainbond clusters.

The tool automates the entire deployment process including:
- Environment detection and initialization
- Base component installation (MySQL, Keepalived)
- Kubernetes (RKE2) deployment
- Rainbond cluster installation`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if verbose {
			fmt.Println("Verbose mode enabled")
		}
	},
	CompletionOptions: cobra.CompletionOptions{
		DisableDefaultCmd: true,
	},
}

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Set up or update a Rainbond cluster with optional operations",
	Long: `Set up or update a Rainbond cluster with support for various operations:
  --check         Check system environment and requirements only
  --lvm           Show LVM status and create LVM configuration only
  --rke2          Install and configure RKE2 Kubernetes cluster only
  --mysql         Install and configure MySQL master-slave cluster only
  --rainbond      Install and configure Rainbond only
  --optimize      Optimize system for containerized environments only

默认行为（不使用任何flags时）：
  roi up     # 依次执行: 系统检查 -> LVM配置 -> 系统优化 -> RKE2安装 -> MySQL安装 -> Rainbond安装

单独执行某个阶段：
  roi up --check           # 仅执行系统检查
  roi up --lvm             # 仅执行LVM配置
  roi up --rke2            # 仅执行RKE2 Kubernetes安装
  roi up --mysql           # 仅执行MySQL主从集群安装
  roi up --rainbond        # 仅执行Rainbond安装
  roi up --optimize        # 仅执行系统优化`,
	RunE: func(cmd *cobra.Command, args []string) error {
		configFile := cfgFile
		if configFile == "" {
			configFile = viper.ConfigFileUsed()
			if configFile == "" {
				return fmt.Errorf("config file not found. Please specify with --config flag or create ./config.yaml")
			}
		}

		cfg, err := config.LoadConfig(configFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		// Execute specific operations based on flags
		if checkFlag {
			return runCheck(cfg)
		}

		if lvmFlag {
			return runLVM(cfg)
		}

		if rke2Flag {
			return runRKE2(cfg)
		}

		if optimizeFlag {
			return runOptimize(cfg)
		}

		if mysqlFlag {
			return runMySQL(cfg)
		}

		if rainbondFlag {
			return runRainbond(cfg)
		}

		// Default: full installation - execute all stages in order
		fmt.Println("\033[36m[INFO]\033[0m 欢迎使用 Rainbond 命令行安装工具！")

		// 初始化日志记录器，详细日志记录到文件，控制台只显示进度和错误
		appLogger, err := logger.NewProgressLogger() // 控制台只显示ERROR，文件记录所有DEBUG信息
		if err != nil {
			return fmt.Errorf("初始化日志记录器失败: %w", err)
		}
		defer appLogger.Close()

		// 初始化步骤进度显示器，集成logger
		stepProgress := progress.NewStepProgressWithLogger(6, appLogger)

		// 设置主机IP列表
		var hostIPs []string
		for _, host := range cfg.Hosts {
			hostIPs = append(hostIPs, host.IP)
		}
		stepProgress.SetHostIPs(hostIPs)

		// 阶段1: 系统检查
		stepProgress.StartStep("系统检查")
		appLogger.Info("开始系统检查阶段")
		if err := runCheckWithLogger(cfg, appLogger, stepProgress); err != nil {
			appLogger.Error("系统检查阶段失败: %v", err)
			stepProgress.FailStep(err.Error())
			return fmt.Errorf("系统检查阶段失败: %w", err)
		}
		stepProgress.CompleteStep()
		appLogger.Info("系统检查阶段完成")

		// 阶段2: LVM配置
		stepProgress.StartStep("LVM配置")
		appLogger.Info("开始LVM配置阶段")
		stepProgress.UpdateStepProgress("配置LVM逻辑卷...")
		time.Sleep(500 * time.Millisecond) // 让spinner有时间显示
		if err := runLVMWithLogger(cfg, appLogger, stepProgress); err != nil {
			appLogger.Error("LVM配置阶段失败: %v", err)
			stepProgress.FailStep(err.Error())
			return fmt.Errorf("LVM配置阶段失败: %w", err)
		}
		stepProgress.CompleteStep()
		appLogger.Info("LVM配置阶段完成")

		// 阶段3: 系统优化
		stepProgress.StartStep("系统优化")
		appLogger.Info("开始系统优化阶段")
		stepProgress.UpdateStepProgress("优化系统配置...")
		time.Sleep(500 * time.Millisecond) // 让spinner有时间显示
		if err := runOptimizeWithLogger(cfg, appLogger, stepProgress); err != nil {
			appLogger.Error("系统优化阶段失败: %v", err)
			stepProgress.FailStep(err.Error())
			return fmt.Errorf("系统优化阶段失败: %w", err)
		}
		stepProgress.CompleteStep()
		appLogger.Info("系统优化阶段完成")

		// 阶段4: RKE2安装
		stepProgress.StartStep("RKE2安装")
		appLogger.Info("开始RKE2安装阶段")
		stepProgress.UpdateStepProgress("安装RKE2 Kubernetes集群...")
		time.Sleep(500 * time.Millisecond) // 让spinner有时间显示
		if err := runRKE2WithLogger(cfg, appLogger, stepProgress); err != nil {
			appLogger.Error("RKE2安装阶段失败: %v", err)
			stepProgress.FailStep(err.Error())
			return fmt.Errorf("RKE2安装阶段失败: %w", err)
		}
		stepProgress.CompleteStep()
		appLogger.Info("RKE2安装阶段完成")

		// 阶段5: MySQL安装
		stepProgress.StartStep("MySQL安装")
		appLogger.Info("开始MySQL安装阶段")
		stepProgress.UpdateStepProgress("安装MySQL数据库...")
		time.Sleep(500 * time.Millisecond) // 让spinner有时间显示
		if err := runMySQLWithLogger(cfg, appLogger, stepProgress); err != nil {
			appLogger.Error("MySQL安装阶段失败: %v", err)
			stepProgress.FailStep(err.Error())
			return fmt.Errorf("MySQL安装阶段失败: %w", err)
		}
		stepProgress.CompleteStep()
		appLogger.Info("MySQL安装阶段完成")

		// 阶段6: Rainbond安装
		stepProgress.StartStep("Rainbond安装")
		appLogger.Info("开始Rainbond安装阶段")
		stepProgress.UpdateStepProgress("安装Rainbond平台...")
		time.Sleep(500 * time.Millisecond) // 让spinner有时间显示
		if err := runRainbondWithLogger(cfg, appLogger, stepProgress); err != nil {
			appLogger.Error("Rainbond安装阶段失败: %v", err)
			stepProgress.FailStep(err.Error())
			return fmt.Errorf("Rainbond安装阶段失败: %w", err)
		}
		stepProgress.CompleteStep()
		appLogger.Info("Rainbond安装阶段完成")

		// 完成所有步骤，重新启用控制台输出
		stepProgress.Finish()

		// 显示安装成功总结
		fmt.Println("=====================================================")
		fmt.Println("\033[32m 🎉 Rainbond 安装成功！🎉 \033[0m")

		// 获取第一个主机的IP作为访问地址
		var accessIP string
		if len(cfg.Hosts) > 0 {
			accessIP = cfg.Hosts[0].IP
		} else {
			accessIP = "<未配置主机IP>"
		}

		fmt.Printf("\033[32m 访问地址: http://%s:7070 \033[0m\n", accessIP)
		fmt.Println("")
		fmt.Printf("详细日志文件: %s\n", appLogger.GetLogFilePath())
		fmt.Println("\033[32m 🙏 感谢使用 Rainbond！ 🙏\033[0m")
		fmt.Println("=====================================================")
		return nil
	},
}

func runCheck(cfg *config.Config) error {
	checker := check.NewBasicChecker(cfg)
	return checker.Run()
}

func runLVM(cfg *config.Config) error {
	lvmManager := lvm.NewLVM(cfg)
	return lvmManager.ShowAndCreate()
}

func runRKE2(cfg *config.Config) error {
	rke2Installer := rke2.NewRKE2Installer(cfg)
	return rke2Installer.Run()
}

func runOptimize(cfg *config.Config) error {
	optimizer := optimize.NewSystemOptimizer(cfg)
	return optimizer.Run()
}

func runMySQL(cfg *config.Config) error {
	mysqlInstaller := mysql.NewMySQLInstaller(cfg)
	return mysqlInstaller.Run()
}

func runRainbond(cfg *config.Config) error {
	rainbondInstaller := rainbond.NewRainbondInstaller(cfg)
	return rainbondInstaller.Run()
}

// 带有日志记录器的运行函数
func runCheckWithLogger(cfg *config.Config, logger *logger.Logger, stepProgress *progress.StepProgress) error {
	logger.Info("系统检查: 开始环境检测")
	stepProgress.UpdateStepProgress("检测系统环境...")
	checker := check.NewBasicCheckerWithLoggerAndProgress(cfg, logger, stepProgress)
	return checker.Run()
}

func runLVMWithLogger(cfg *config.Config, logger *logger.Logger, stepProgress *progress.StepProgress) error {
	logger.Info("LVM配置: 检查并配置逻辑卷管理")
	stepProgress.UpdateStepProgress("配置LVM逻辑卷...")

	// 检查是否有LVM配置
	hasLVMConfig := false
	for _, host := range cfg.Hosts {
		if host.LVMConfig != nil && len(host.LVMConfig.PVDevices) > 0 {
			hasLVMConfig = true
			break
		}
	}

	if !hasLVMConfig {
		stepProgress.SkipStep("未找到 LVM 配置")
		return nil
	}

	lvmManager := lvm.NewLVMWithLogger(cfg, logger)
	return lvmManager.ShowAndCreate()
}

func runRKE2WithLogger(cfg *config.Config, logger *logger.Logger, stepProgress *progress.StepProgress) error {
	logger.Info("RKE2安装: 开始Kubernetes集群部署")
	stepProgress.UpdateStepProgress("安装RKE2 Kubernetes集群...")
	rke2Installer := rke2.NewRKE2InstallerWithLoggerAndProgress(cfg, logger, stepProgress)
	return rke2Installer.Run()
}

func runOptimizeWithLogger(cfg *config.Config, logger *logger.Logger, stepProgress *progress.StepProgress) error {
	logger.Info("系统优化: 优化容器环境配置")
	stepProgress.UpdateStepProgress("优化系统配置...")
	optimizer := optimize.NewSystemOptimizerWithLoggerAndProgress(cfg, logger, stepProgress)
	return optimizer.Run()
}

func runMySQLWithLogger(cfg *config.Config, logger *logger.Logger, stepProgress *progress.StepProgress) error {
	logger.Info("MySQL安装: 部署MySQL主从集群")
	stepProgress.UpdateStepProgress("安装MySQL数据库...")

	// 检查是否有MySQL配置或MySQL节点
	hasMySQLConfig := cfg.MySQL.Enabled
	if !hasMySQLConfig {
		// 检查是否有MySQL主节点或从节点
		for _, host := range cfg.Hosts {
			if host.MySQLMaster || host.MySQLSlave {
				hasMySQLConfig = true
				break
			}
		}
	}

	if !hasMySQLConfig {
		stepProgress.SkipStep("未找到 MySQL 配置或 MySQL 节点")
		return nil
	}

	mysqlInstaller := mysql.NewMySQLInstallerWithLoggerAndProgress(cfg, logger, stepProgress)
	return mysqlInstaller.Run()
}

func runRainbondWithLogger(cfg *config.Config, logger *logger.Logger, stepProgress *progress.StepProgress) error {
	logger.Info("Rainbond安装: 部署Rainbond应用管理平台")
	stepProgress.UpdateStepProgress("安装Rainbond平台...")
	rainbondInstaller := rainbond.NewRainbondInstallerWithLoggerAndProgress(cfg, logger, stepProgress)
	return rainbondInstaller.Run()
}

var sshSetupCmd = &cobra.Command{
	Use:   "ssh-setup",
	Short: "Configure SSH passwordless access for all hosts",
	Long: `Configure SSH passwordless access to all hosts defined in the configuration file.
	
This command helps you set up SSH key-based authentication to avoid password prompts 
during cluster installation. It supports multiple methods for different environments.

Options:
  --unified-password  Batch mode - configure all hosts sequentially
  --force-generate    Force generate new SSH key pair even if one exists
  --method           SSH setup method: auto, ssh-copy-id, expect, native-go

SSH Methods:
  auto        Auto-detect best method (default)
  ssh-copy-id Standard ssh-copy-id tool (interactive password input)
  expect      Automated using expect scripts (requires expect to be installed)
  native-go   Go native SSH client (requires password parameter)

Usage examples:
  roi ssh-setup                           # Auto-detect and interactive mode
  roi ssh-setup --unified-password        # Use same password for all hosts
  roi ssh-setup --method=expect           # Use expect scripts
  roi ssh-setup --method=native-go        # Use Go native SSH client
  roi ssh-setup --force-generate          # Generate new SSH key pair first

The command will:
1. Generate SSH key pair (id_rsa) if public key doesn't exist (or --force-generate is used)
2. Copy public key to each host using selected method
3. Test SSH connection to verify passwordless access works

Note: 
- With --unified-password: You'll be prompted once for password to use on all hosts
- Without --unified-password: You'll be prompted for each host individually
- You'll need to manually update your config file with the SSH key path after setup`,
	RunE: func(cmd *cobra.Command, args []string) error {
		configFile := cfgFile
		if configFile == "" {
			configFile = viper.ConfigFileUsed()
			if configFile == "" {
				return fmt.Errorf("config file not found. Please specify with --config flag or create ./config.yaml")
			}
		}

		cfg, err := config.LoadConfig(configFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		return runSSHSetup(cfg, configFile)
	},
}

func runSSHSetup(cfg *config.Config, configFile string) error {
	fmt.Println("🔑 SSH免密配置工具")
	fmt.Println(strings.Repeat("=", 50))
	
	if len(cfg.Hosts) == 0 {
		return fmt.Errorf("配置文件中未找到主机列表")
	}
	
	fmt.Printf("发现 %d 台主机需要配置SSH免密:\n", len(cfg.Hosts))
	for i, host := range cfg.Hosts {
		fmt.Printf("  %d. %s (%s@%s)\n", i+1, host.IP, host.User, host.IP)
	}
	fmt.Println()
	
	// 根据用户选择或自动检测SSH设置方法
	var method ssh.SetupSSHMethod
	var methodName string
	
	switch strings.ToLower(sshMethod) {
	case "ssh-copy-id":
		method = ssh.MethodSSHCopyID
		methodName = "ssh-copy-id"
	case "expect":
		method = ssh.MethodExpect
		methodName = "expect脚本"
	case "native-go":
		method = ssh.MethodNativeGo
		methodName = "Go原生SSH客户端"
	case "auto":
		fallthrough
	default:
		method = ssh.DetectBestSSHMethod()
		switch method {
		case ssh.MethodExpect:
			methodName = "expect脚本 (自动选择)"
		case ssh.MethodSSHCopyID:
			methodName = "ssh-copy-id (自动选择)"
		default:
			methodName = "ssh-copy-id (默认)"
			method = ssh.MethodSSHCopyID
		}
	}
	
	fmt.Printf("🔧 使用方法: %s\n\n", methodName)
	
	// 设置SSH配置选项
	options := ssh.SSHSetupOptions{
		Method:          method,
		UnifiedPassword: sshUnifiedPassword,
		ForceGenerate:   sshForceGenerate,
	}
	
	// 配置SSH免密登录
	keyPair, err := ssh.SetupSSHForHosts(cfg.Hosts, options)
	if err != nil {
		return fmt.Errorf("SSH免密配置失败: %w", err)
	}
	
	fmt.Println("\n🎉 SSH免密配置完成！")
	fmt.Printf("📋 私钥路径: %s\n", keyPair.PrivateKeyPath)
	fmt.Printf("📋 公钥路径: %s\n", keyPair.PublicKeyPath)
	fmt.Println("\n💡 提示: 请在配置文件中手动设置 ssh_key 字段为私钥路径")
	
	return nil
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default search: ./config.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")

	upCmd.Flags().BoolVar(&checkFlag, "check", false, "Check system environment and requirements")
	upCmd.Flags().BoolVar(&lvmFlag, "lvm", false, "Show LVM status and create LVM configuration")
	upCmd.Flags().BoolVar(&rke2Flag, "rke2", false, "Install and configure RKE2 Kubernetes cluster")
	upCmd.Flags().BoolVar(&mysqlFlag, "mysql", false, "Install and configure MySQL master-slave cluster")
	upCmd.Flags().BoolVar(&rainbondFlag, "rainbond", false, "Install and configure Rainbond")
	upCmd.Flags().BoolVar(&optimizeFlag, "optimize", false, "Optimize system for containerized environments")

	sshSetupCmd.Flags().BoolVar(&sshUnifiedPassword, "unified-password", false, "All hosts use the same password")
	sshSetupCmd.Flags().BoolVar(&sshForceGenerate, "force-generate", false, "Force generate new SSH key pair")
	sshSetupCmd.Flags().StringVar(&sshMethod, "method", "auto", "SSH setup method: auto, ssh-copy-id, expect, native-go")

	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(sshSetupCmd)
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.AddConfigPath(".") // 当前目录
		viper.SetConfigType("yaml")
		viper.SetConfigName("config") // 移除点号，支持 roi.yaml 和 .roi.yaml
	}

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		if verbose {
			fmt.Println("Using config file:", viper.ConfigFileUsed())
		}
	}
}

func main() {
	if err := Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
