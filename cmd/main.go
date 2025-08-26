package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/rainbond/rainbond-offline-installer/internal/check"
	"github.com/rainbond/rainbond-offline-installer/internal/lvm"
	"github.com/rainbond/rainbond-offline-installer/internal/mysql"
	"github.com/rainbond/rainbond-offline-installer/internal/optimize"
	"github.com/rainbond/rainbond-offline-installer/internal/rainbond"
	"github.com/rainbond/rainbond-offline-installer/internal/rke2"
	"github.com/rainbond/rainbond-offline-installer/pkg/config"
)

var (
	cfgFile string
	verbose bool
)

var (
	checkFlag     bool
	lvmFlag       bool
	optimizeFlag  bool
	rke2Flag      bool
	mysqlFlag     bool
	rainbondFlag  bool
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

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install Rainbond cluster with optional operations",
	Long: `Install Rainbond cluster with support for various operations:
  --check         Check system environment and requirements only
  --lvm           Show LVM status and create LVM configuration only
  --rke2          Install and configure RKE2 Kubernetes cluster only
  --mysql         Install and configure MySQL master-slave cluster only
  --rainbond      Install and configure Rainbond only
  --optimize      Optimize system for containerized environments only

默认行为（不使用任何flags时）：
  roi install     # 依次执行: 系统检查 -> LVM配置 -> 系统优化 -> RKE2安装 -> MySQL安装 -> Rainbond安装

单独执行某个阶段：
  roi install --check           # 仅执行系统检查
  roi install --lvm             # 仅执行LVM配置
  roi install --rke2            # 仅执行RKE2 Kubernetes安装
  roi install --mysql           # 仅执行MySQL主从集群安装
  roi install --rainbond        # 仅执行Rainbond安装
  roi install --optimize        # 仅执行系统优化`,
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
		// 阶段1: 系统检查
		if err := runCheck(cfg); err != nil {
			return fmt.Errorf("系统检查阶段失败: %w", err)
		}

		// 阶段2: LVM配置
		if err := runLVM(cfg); err != nil {
			return fmt.Errorf("LVM配置阶段失败: %w", err)
		}

		// 阶段3: 系统优化
		if err := runOptimize(cfg); err != nil {
			return fmt.Errorf("系统优化阶段失败: %w", err)
		}

		// 阶段4: RKE2安装
		if err := runRKE2(cfg); err != nil {
			return fmt.Errorf("RKE2安装阶段失败: %w", err)
		}

		// 阶段5: MySQL安装
		if err := runMySQL(cfg); err != nil {
			return fmt.Errorf("MySQL安装阶段失败: %w", err)
		}

		// 阶段6: Rainbond安装
		if err := runRainbond(cfg); err != nil {
			return fmt.Errorf("Rainbond安装阶段失败: %w", err)
		}

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

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default search: ./config.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")

	installCmd.Flags().BoolVar(&checkFlag, "check", false, "Check system environment and requirements")
	installCmd.Flags().BoolVar(&lvmFlag, "lvm", false, "Show LVM status and create LVM configuration")
	installCmd.Flags().BoolVar(&rke2Flag, "rke2", false, "Install and configure RKE2 Kubernetes cluster")
	installCmd.Flags().BoolVar(&mysqlFlag, "mysql", false, "Install and configure MySQL master-slave cluster")
	installCmd.Flags().BoolVar(&rainbondFlag, "rainbond", false, "Install and configure Rainbond")
	installCmd.Flags().BoolVar(&optimizeFlag, "optimize", false, "Optimize system for containerized environments")

	rootCmd.AddCommand(installCmd)
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