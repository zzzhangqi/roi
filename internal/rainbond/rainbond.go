package rainbond

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rainbond/rainbond-offline-installer/pkg/config"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
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

type RainbondInstaller struct {
	config       *config.Config
	logger       Logger
	stepProgress StepProgress
	chartPath    string
	kubeConfig   *rest.Config
	kubeClient   kubernetes.Interface
	helmSettings *cli.EnvSettings
	actionConfig *action.Configuration
}

func NewRainbondInstaller(cfg *config.Config) *RainbondInstaller {
	return NewRainbondInstallerWithLogger(cfg, nil)
}

func NewRainbondInstallerWithLogger(cfg *config.Config, logger Logger) *RainbondInstaller {
	return NewRainbondInstallerWithLoggerAndProgress(cfg, logger, nil)
}

func NewRainbondInstallerWithLoggerAndProgress(cfg *config.Config, logger Logger, stepProgress StepProgress) *RainbondInstaller {
	r := &RainbondInstaller{
		config:       cfg,
		logger:       logger,
		stepProgress: stepProgress,
		chartPath:    "./rainbond.tgz", // 使用tgz包
	}
	// 初始化Kubernetes客户端和Helm配置
	if err := r.initializeClients(); err != nil {
		if logger != nil {
			logger.Error("初始化Kubernetes和Helm客户端失败: %v", err)
		}
	}
	return r
}

func (r *RainbondInstaller) SetChartPath(path string) {
	r.chartPath = path
}

// 初始化Kubernetes客户端和Helm配置
func (r *RainbondInstaller) initializeClients() error {
	// 获取kubeconfig
	kubeConfigPath, err := r.getKubeConfig()
	if err != nil {
		return fmt.Errorf("获取kubeconfig失败: %w", err)
	}

	if r.logger != nil {
		r.logger.Debug("使用kubeconfig文件: %s", kubeConfigPath)
	}

	// 创建Kubernetes配置
	config, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
	if err != nil {
		return fmt.Errorf("构建Kubernetes配置失败: %w", err)
	}
	r.kubeConfig = config

	if r.logger != nil {
		r.logger.Debug("Kubernetes API服务器地址: %s", config.Host)
	}

	// 创建Kubernetes客户端
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("创建Kubernetes客户端失败: %w", err)
	}
	r.kubeClient = clientset

	// 测试连接
	if r.logger != nil {
		r.logger.Debug("测试Kubernetes集群连接...")
	}
	nodes, err := clientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("测试Kubernetes连接失败: %w", err)
	}
	if r.logger != nil {
		r.logger.Debug("成功连接到Kubernetes集群，发现 %d 个节点", len(nodes.Items))
	}

	// 初始化Helm设置，明确指定kubeconfig路径
	r.helmSettings = cli.New()
	r.helmSettings.KubeConfig = kubeConfigPath

	if r.logger != nil {
		r.logger.Debug("Helm设置kubeconfig路径: %s", kubeConfigPath)
	}

	// 创建Helm action配置
	actionConfig := new(action.Configuration)

	// 指定rbd-system作为默认命名空间，使用默认存储（secret）
	if err := actionConfig.Init(r.helmSettings.RESTClientGetter(), "rbd-system", "", func(format string, v ...interface{}) {
		if r.logger != nil {
			r.logger.Debug("[Helm] %s", fmt.Sprintf(format, v...))
		}
	}); err != nil {
		return fmt.Errorf("初始化Helm action配置失败: %w", err)
	}
	r.actionConfig = actionConfig

	// 验证Helm配置是否正常工作
	if r.logger != nil {
		r.logger.Debug("验证Helm配置...")
	}
	testList := action.NewList(actionConfig)
	testList.SetStateMask()
	_, testErr := testList.Run()
	if testErr != nil {
		if r.logger != nil {
			r.logger.Warn("Helm配置测试失败: %v", testErr)
		}
		// 不返回错误，继续执行，但记录警告
	} else {
		if r.logger != nil {
			r.logger.Debug("Helm配置验证成功")
		}
	}

	return nil
}

// 获取kubeconfig文件路径
func (r *RainbondInstaller) getKubeConfig() (string, error) {
	// 优先使用RKE2模块保存的本地kubeconfig文件
	localKubeConfigPath := "./kubeconfig"

	// 检查本地kubeconfig是否存在
	if _, err := os.Stat(localKubeConfigPath); err == nil {
		if r.logger != nil {
			r.logger.Info("使用RKE2安装时保存的本地kubeconfig文件")
		}
		return localKubeConfigPath, nil
	}

	// 如果本地文件不存在，回退到从远程获取（兼容性）
	controlNode := r.config.Hosts[0]
	fallbackPath := "/tmp/kubeconfig"

	if r.logger != nil {
		r.logger.Warn("本地kubeconfig文件不存在，从控制节点 %s 获取kubeconfig...", controlNode.IP)
	}

	// 使用scp复制kubeconfig到本地
	var scpCmd []string
	if controlNode.Password != "" {
		scpCmd = []string{"sshpass", "-p", controlNode.Password, "scp",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			fmt.Sprintf("%s@%s:/etc/rancher/rke2/rke2.yaml", controlNode.User, controlNode.IP),
			fallbackPath}
	} else if controlNode.SSHKey != "" {
		scpCmd = []string{"scp", "-i", controlNode.SSHKey,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			fmt.Sprintf("%s@%s:/etc/rancher/rke2/rke2.yaml", controlNode.User, controlNode.IP),
			fallbackPath}
	} else {
		scpCmd = []string{"scp",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			fmt.Sprintf("%s@%s:/etc/rancher/rke2/rke2.yaml", controlNode.User, controlNode.IP),
			fallbackPath}
	}

	// 执行scp命令
	cmd := r.buildCommand(scpCmd[0], scpCmd[1:]...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("复制kubeconfig失败: %w, 输出: %s", err, string(output))
	}

	// 修改server地址为实际IP
	if err := r.updateKubeConfigServer(fallbackPath, controlNode.IP); err != nil {
		return "", fmt.Errorf("更新kubeconfig server地址失败: %w", err)
	}

	return fallbackPath, nil
}

// 更新kubeconfig中的server地址
func (r *RainbondInstaller) updateKubeConfigServer(kubeconfigPath, serverIP string) error {
	// 读取kubeconfig文件
	data, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		return err
	}

	// 替换server地址
	content := strings.ReplaceAll(string(data), "https://127.0.0.1:6443", fmt.Sprintf("https://%s:6443", serverIP))

	// 写回文件
	return os.WriteFile(kubeconfigPath, []byte(content), 0644)
}

// 执行命令的通用方法
func (r *RainbondInstaller) executeCommand(name string, args ...string) error {
	cmd := r.buildCommand(name, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("命令执行失败: %w, 输出: %s", err, string(output))
	}
	return nil
}

func (r *RainbondInstaller) Run() error {
	if r.logger != nil {
		r.logger.Info("开始安装Rainbond...")
	}

	// 确保客户端已初始化
	if r.kubeClient == nil || r.actionConfig == nil {
		if r.logger != nil {
			r.logger.Debug("初始化Kubernetes和Helm客户端...")
		}
		if err := r.initializeClients(); err != nil {
			return fmt.Errorf("初始化客户端失败: %w", err)
		}
		if r.logger != nil {
			r.logger.Debug("客户端初始化成功")
		}
	}

	// 检查Kubernetes集群状态
	if err := r.checkKubernetesReady(); err != nil {
		return fmt.Errorf("Kubernetes集群未就绪: %w", err)
	}

	// 检查现有Rainbond部署
	if exists, err := r.checkExistingDeployment(); err != nil {
		return fmt.Errorf("检查现有Rainbond部署失败: %w", err)
	} else if exists {
		if r.logger != nil {
			r.logger.Info("检测到Rainbond已存在，跳过安装")
		}
		return nil
	}

	// 创建命名空间
	if err := r.createNamespace(); err != nil {
		return fmt.Errorf("创建命名空间失败: %w", err)
	}

	// 生成values配置
	values, err := r.generateValues()
	if err != nil {
		return fmt.Errorf("生成values配置失败: %w", err)
	}

	// 安装Rainbond Helm Chart
	if err := r.installHelmChart(values); err != nil {
		return fmt.Errorf("安装Rainbond失败: %w", err)
	}

	if r.logger != nil {
		r.logger.Info("🎉 Rainbond Helm安装完成!")
	}
	return nil
}

func (r *RainbondInstaller) checkKubernetesReady() error {
	if r.logger != nil {
		r.logger.Info("检查Kubernetes集群状态...")
	}

	// 使用Kubernetes客户端直接检查节点状态
	nodes, err := r.kubeClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("获取节点列表失败: %w", err)
	}

	readyNodes := 0
	for _, node := range nodes.Items {
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				readyNodes++
				break
			}
		}
	}

	if readyNodes == 0 {
		return fmt.Errorf("没有就绪的节点")
	}

	if r.logger != nil {
		r.logger.Info("Kubernetes集群已就绪，%d 个节点就绪", readyNodes)
	}
	return nil
}

func (r *RainbondInstaller) checkExistingDeployment() (bool, error) {
	if r.logger != nil {
		r.logger.Info("检查现有Rainbond部署...")
	}

	namespace := r.config.Rainbond.Namespace
	if namespace == "" {
		namespace = "rbd-system"
	}

	// 确保actionConfig已初始化
	if r.actionConfig == nil {
		return false, fmt.Errorf("Helm action配置未初始化")
	}

	if r.logger != nil {
		r.logger.Debug("使用Helm API检查现有部署，目标命名空间: %s", namespace)
	}

	// 使用Helm API检查是否已安装
	list := action.NewList(r.actionConfig)
	list.SetStateMask()
	releases, err := list.Run()
	if err != nil {
		if r.logger != nil {
			r.logger.Error("Helm列表查询失败: %v", err)
		}
		return false, fmt.Errorf("查询Helm releases失败: %w", err)
	}

	if r.logger != nil {
		r.logger.Debug("发现 %d 个Helm releases", len(releases))
	}

	for _, rel := range releases {
		if rel.Name == "rainbond" && rel.Namespace == namespace {
			return true, nil
		}
	}

	return false, nil
}

func (r *RainbondInstaller) createNamespace() error {
	namespace := r.config.Rainbond.Namespace
	if namespace == "" {
		namespace = "rbd-system"
	}

	if r.logger != nil {
		r.logger.Info("创建命名空间 %s...", namespace)
	}

	// 使用Kubernetes客户端检查命名空间是否已存在
	_, err := r.kubeClient.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err == nil {
		if r.logger != nil {
			r.logger.Info("命名空间 %s 已存在，跳过创建", namespace)
		}
		return nil
	}

	// 创建命名空间
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}

	_, err = r.kubeClient.CoreV1().Namespaces().Create(context.TODO(), ns, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("创建命名空间失败: %w", err)
	}

	if r.logger != nil {
		r.logger.Info("命名空间 %s 创建成功", namespace)
	}
	return nil
}

func (r *RainbondInstaller) generateValues() (map[string]interface{}, error) {
	if r.logger != nil {
		r.logger.Info("生成Helm values配置...")
	}

	// 合并默认配置和用户配置
	values := make(map[string]interface{})

	// 设置基础配置
	namespace := r.config.Rainbond.Namespace
	if namespace == "" {
		namespace = "rbd-system"
	}

	// 如果用户有自定义values，使用用户的配置
	if r.config.Rainbond.Values != nil {
		values = r.config.Rainbond.Values
	}

	// 如果启用了MySQL，自动配置数据库连接
	if r.config.MySQL.Enabled {
		if r.logger != nil {
			r.logger.Info("检测到MySQL已启用，自动配置数据库连接...")
		}

		cluster, ok := values["Cluster"].(map[string]interface{})
		if !ok {
			cluster = make(map[string]interface{})
			values["Cluster"] = cluster
		}

		// 配置region数据库
		cluster["regionDatabase"] = map[string]interface{}{
			"enable":   true,
			"host":     "mysql-master.rbd-system.svc.cluster.local",
			"port":     3306,
			"name":     "region",
			"username": "root",
			"password": r.config.MySQL.RootPassword,
		}

		// 配置console数据库
		cluster["uiDatabase"] = map[string]interface{}{
			"enable":   true,
			"host":     "mysql-master.rbd-system.svc.cluster.local",
			"port":     3306,
			"name":     "console",
			"username": "root",
			"password": r.config.MySQL.RootPassword,
		}
	}

	if r.logger != nil {
		r.logger.Info("Values配置生成完成")
	}
	return values, nil
}

func (r *RainbondInstaller) installHelmChart(values map[string]interface{}) error {
	if r.logger != nil {
		r.logger.Info("开始安装Rainbond Helm Chart...")
	}

	namespace := r.config.Rainbond.Namespace
	if namespace == "" {
		namespace = "rbd-system"
	}

	releaseName := "rainbond"

	// 检查chart包是否存在
	if _, err := os.Stat(r.chartPath); os.IsNotExist(err) {
		return fmt.Errorf("chart包不存在: %s", r.chartPath)
	}

	// 加载chart
	chart, err := loader.Load(r.chartPath)
	if err != nil {
		return fmt.Errorf("加载chart失败: %w", err)
	}

	// 创建Helm install action
	install := action.NewInstall(r.actionConfig)
	install.ReleaseName = releaseName
	install.Namespace = namespace
	install.CreateNamespace = true
	install.Wait = true
	install.Timeout = 20 * time.Minute // 20分钟超时

	if r.logger != nil {
		r.logger.Info("使用Helm SDK安装chart: %s 到命名空间: %s", releaseName, namespace)
	}

	// 执行安装
	rel, err := install.Run(chart, values)
	if err != nil {
		if r.logger != nil {
			r.logger.Error("Helm安装失败: %v", err)
		}
		return fmt.Errorf("helm install失败: %w", err)
	}

	if r.logger != nil {
		r.logger.Info("Rainbond Helm Chart安装成功")
		r.logger.Info("Release名称: %s, 版本: %d, 状态: %s",
			rel.Name, rel.Version, rel.Info.Status)
	}
	return nil
}

// buildCommand 构建命令的通用方法
func (r *RainbondInstaller) buildCommand(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
