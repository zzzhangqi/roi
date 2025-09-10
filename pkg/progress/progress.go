package progress

import (
	"fmt"
	"strings"
	"time"
)

type ProgressBar struct {
	total   int
	current int
	width   int
	prefix  string
	suffix  string
	fill    string
	head    string
	empty   string
}

func NewProgressBar(total int, prefix string) *ProgressBar {
	return &ProgressBar{
		total: total,
		width: 50,
		prefix: prefix,
		fill:   "█",
		head:   "█", 
		empty:  "░",
	}
}

func (pb *ProgressBar) Update(current int) {
	pb.current = current
	pb.render()
}

func (pb *ProgressBar) Increment() {
	pb.current++
	pb.render()
}

func (pb *ProgressBar) render() {
	percent := float64(pb.current) / float64(pb.total)
	filled := int(percent * float64(pb.width))
	
	bar := strings.Repeat(pb.fill, filled) + strings.Repeat(pb.empty, pb.width-filled)
	
	fmt.Printf("\r%s [%s] %d%% (%d/%d)", 
		pb.prefix, 
		bar, 
		int(percent*100), 
		pb.current, 
		pb.total)
}

func (pb *ProgressBar) Finish() {
	pb.current = pb.total
	pb.render()
	fmt.Println() // 换行
}

type Spinner struct {
	chars       []string
	current     int
	prefix      string
	suffix      string
	subInfo     string // 子步骤信息
	active      bool
}

func NewSpinner(prefix string) *Spinner {
	return &Spinner{
		chars:  []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		prefix: prefix,
		active: false,
	}
}

func (s *Spinner) Start() {
	s.active = true
	go s.spin()
}

func (s *Spinner) spin() {
	for s.active {
		// spinner在末尾，格式：Step X/Y: [进行中] 步骤名 ⠋
		fmt.Printf("\r%s %s", s.prefix, s.chars[s.current%len(s.chars)])
		s.current++
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Spinner) Stop() {
	s.active = false
	// 清除当前行
	fmt.Print("\r\033[K")
}

func (s *Spinner) Fail() {
	s.active = false
	// 清除当前行
	fmt.Print("\r\033[K")
}

func (s *Spinner) UpdateSuffix(suffix string) {
	s.suffix = suffix
}

func (s *Spinner) UpdateSubInfo(subInfo string) {
	s.subInfo = subInfo
}

// Logger接口，用于与日志系统集成
type Logger interface {
	SuppressConsole()
	EnableConsole()
	InfoToFileOnly(format string, v ...interface{})
}

type StepProgress struct {
	totalSteps     int
	currentStep    int
	stepName       string
	logger         Logger
	isRunning      bool
	spinner        *Spinner
	
	// 子阶段信息
	subInfo        string
	
	// 主机信息
	hostIPs        []string
}

func NewStepProgress(totalSteps int) *StepProgress {
	return &StepProgress{
		totalSteps:  totalSteps,
		currentStep: 0,
	}
}

// NewStepProgressWithLogger 创建带有logger的步骤进度显示器
func NewStepProgressWithLogger(totalSteps int, logger Logger) *StepProgress {
	return &StepProgress{
		totalSteps:  totalSteps,
		currentStep: 0,
		logger:      logger,
	}
}

// SetHostIPs 设置主机IP列表
func (sp *StepProgress) SetHostIPs(hostIPs []string) {
	sp.hostIPs = hostIPs
}

func (sp *StepProgress) StartStep(stepName string) {
	sp.currentStep++
	sp.stepName = stepName
	sp.isRunning = true
	
	// 如果有logger，抑制其控制台输出
	if sp.logger != nil {
		sp.logger.SuppressConsole()
		sp.logger.InfoToFileOnly("开始步骤 %d/%d: %s", sp.currentStep, sp.totalSteps, stepName)
	}
	
	// 先打印一行"开始检查"信息，保留在历史记录中
	fmt.Printf("\033[36m[INFO]\033[0m [\033[33m%s %d/%d\033[0m] %s\n", sp.getStagePrefix(), sp.currentStep, sp.totalSteps, sp.getStartMessage())
	
	// 再打印一行具体处理信息，也保留在历史记录中
	fmt.Printf("\033[36m[INFO]\033[0m [\033[33m%s %d/%d\033[0m] %s\n", sp.getStagePrefix(), sp.currentStep, sp.totalSteps, sp.getProcessingMessage())
	
	// 然后在下一行启动spinner显示进度
	sp.spinner = NewSpinner(fmt.Sprintf("\033[36m[INFO]\033[0m [\033[33m%s %d/%d\033[0m] 进行中", sp.getStagePrefix(), sp.currentStep, sp.totalSteps))
	sp.spinner.Start()
}

// getStagePrefix 根据步骤名称返回对应的阶段前缀
func (sp *StepProgress) getStagePrefix() string {
	switch sp.stepName {
	case "系统检查":
		return "Check Stage"
	case "LVM配置":
		return "LVM Config"
	case "系统优化":
		return "System Optimize"
	case "RKE2安装":
		return "RKE2 Install"
	case "MySQL安装":
		return "MySQL Install"
	case "Rainbond安装":
		return "Rainbond Install"
	default:
		return "Stage"
	}
}

// getStartMessage 根据步骤名称返回对应的开始消息
func (sp *StepProgress) getStartMessage() string {
	switch sp.stepName {
	case "系统检查":
		return "开始检查操作系统基础环境"
	case "LVM配置":
		return "开始检查 LVM 配置"
	case "系统优化":
		return "开始检查系统优化"
	case "RKE2安装":
		return "开始检查 RKE2 安装"
	case "MySQL安装":
		return "开始检查 MySQL 安装"
	case "Rainbond安装":
		return "开始检查 Rainbond 安装"
	default:
		return "开始检查配置"
	}
}

// getProcessingMessage 根据步骤名称返回对应的进行中消息
func (sp *StepProgress) getProcessingMessage() string {
	switch sp.stepName {
	case "系统检查":
		return "检测系统环境和依赖"
	case "LVM配置":
		return "配置逻辑卷管理"
	case "系统优化":
		return "优化系统参数配置"
	case "RKE2安装":
		return "部署 Kubernetes 集群"
	case "MySQL安装":
		return "部署 MySQL 数据库集群"
	case "Rainbond安装":
		return "部署 Rainbond 应用平台"
	default:
		return "处理配置中"
	}
}

func (sp *StepProgress) UpdateStepProgress(message string) {
	// 记录详细进度到文件
	if sp.logger != nil {
		sp.logger.InfoToFileOnly("步骤进度更新: %s", message)
	}
}

// StartSpinnerIfNeeded 如果还没有启动spinner，现在启动它（用于没有子步骤的阶段）
func (sp *StepProgress) StartSpinnerIfNeeded() {
	if sp.spinner == nil {
		sp.spinner = NewSpinner(fmt.Sprintf("Step %d/%d: \033[33m[进行中]\033[0m %s", sp.currentStep, sp.totalSteps, sp.stepName))
		sp.spinner.Start()
	}
}

func (sp *StepProgress) CompleteStep() {
	if sp.spinner != nil {
		sp.spinner.Stop() // 停止spinner并清除当前行
	}
	sp.isRunning = false
	
	// 清除当前行并显示完成信息（覆盖进行中的信息）
	hostInfo := sp.getHostInfo()
	fmt.Printf("\r\033[K\033[36m[INFO]\033[0m [\033[32m%s %d/%d\033[0m] 节点 \033[35m%s\033[0m %s。\n", sp.getStagePrefix(), sp.currentStep, sp.totalSteps, hostInfo, sp.getCompleteMessage())
	
	// 记录完成信息到文件
	if sp.logger != nil {
		sp.logger.InfoToFileOnly("步骤完成: %s", sp.stepName)
	}
}

// SkipStep 跳过步骤
func (sp *StepProgress) SkipStep(reason string) {
	if sp.spinner != nil {
		sp.spinner.Stop() // 停止spinner并清除当前行
	}
	sp.isRunning = false
	
	// 清除当前行并显示跳过信息
	fmt.Printf("\r\033[K\033[36m[INFO]\033[0m [\033[33m%s %d/%d\033[0m] %s，跳过。\n", sp.getStagePrefix(), sp.currentStep, sp.totalSteps, reason)
	
	// 记录跳过信息到文件
	if sp.logger != nil {
		sp.logger.InfoToFileOnly("步骤跳过: %s - %s", sp.stepName, reason)
	}
}

// getHostInfo 获取主机信息显示文本
func (sp *StepProgress) getHostInfo() string {
	if len(sp.hostIPs) == 0 {
		return "（未配置主机）"
	} else if len(sp.hostIPs) == 1 {
		return sp.hostIPs[0]
	} else {
		return fmt.Sprintf("%s 等 %d 个节点", sp.hostIPs[0], len(sp.hostIPs))
	}
}

// getCompleteMessage 根据步骤名称返回对应的完成消息
func (sp *StepProgress) getCompleteMessage() string {
	switch sp.stepName {
	case "系统检查":
		return "基础环境检测通过"
	case "LVM配置":
		return "LVM 配置完成"
	case "系统优化":
		return "系统优化完成"
	case "RKE2安装":
		return "RKE2 安装完成"
	case "MySQL安装":
		return "MySQL 安装完成"
	case "Rainbond安装":
		return "Rainbond 安装完成"
	default:
		return "配置完成"
	}
}

func (sp *StepProgress) FailStep(errorMsg string) {
	if sp.spinner != nil {
		sp.spinner.Fail() // 停止spinner并清除当前行
	}
	sp.isRunning = false
	
	// 清除当前行并显示失败信息（覆盖进行中的信息）
	hostInfo := sp.getHostInfo()
	fmt.Printf("\r\033[K\033[36m[INFO]\033[0m [\033[31m%s %d/%d\033[0m] 节点 \033[35m%s\033[0m %s失败。原因：\033[31m%s\033[0m\n", sp.getStagePrefix(), sp.currentStep, sp.totalSteps, hostInfo, sp.getCompleteMessage(), errorMsg)
	
	// 记录失败信息到文件，并重新启用控制台输出显示错误
	if sp.logger != nil {
		sp.logger.InfoToFileOnly("步骤失败: %s - %s", sp.stepName, errorMsg)
		sp.logger.EnableConsole() // 失败时重新启用控制台输出
	}
}

// Finish 完成所有步骤，重新启用控制台输出
func (sp *StepProgress) Finish() {
	if sp.logger != nil {
		sp.logger.EnableConsole()
		sp.logger.InfoToFileOnly("所有步骤完成")
	}
}

// StartSubSteps 开始子步骤（仅记录到文件）
func (sp *StepProgress) StartSubSteps(totalSubSteps int) {
	// 子步骤信息只记录到文件，控制台保持简洁
	if sp.logger != nil {
		sp.logger.InfoToFileOnly("开始子步骤组，共 %d 个子步骤", totalSubSteps)
	}
}

// StartSubStep 开始具体的子步骤（仅记录到文件）
func (sp *StepProgress) StartSubStep(subStepName string) {
	// 子步骤不在控制台显示，仅记录到文件
	if sp.logger != nil {
		sp.logger.InfoToFileOnly("执行子步骤: %s", subStepName)
	}
}

// CompleteSubStep 完成子步骤（仅记录到文件）
func (sp *StepProgress) CompleteSubStep() {
	// 子步骤完成信息只记录到文件
}

// CompleteSubSteps 完成所有子步骤（仅记录到文件）
func (sp *StepProgress) CompleteSubSteps() {
	if sp.logger != nil {
		sp.logger.InfoToFileOnly("所有子步骤完成")
	}
}