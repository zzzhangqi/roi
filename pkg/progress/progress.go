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
	chars   []string
	current int
	prefix  string
	suffix  string
	active  bool
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
		fmt.Printf("\r%s %s %s", s.prefix, s.chars[s.current%len(s.chars)], s.suffix)
		s.current++
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Spinner) Stop() {
	s.active = false
	fmt.Printf("\r%s ✅ %s\n", s.prefix, s.suffix)
}

func (s *Spinner) Fail() {
	s.active = false
	fmt.Printf("\r%s ❌ %s\n", s.prefix, s.suffix)
}

func (s *Spinner) UpdateSuffix(suffix string) {
	s.suffix = suffix
}

// Logger接口，用于与日志系统集成
type Logger interface {
	SuppressConsole()
	EnableConsole()
	InfoToFileOnly(format string, v ...interface{})
}

type StepProgress struct {
	totalSteps  int
	currentStep int
	stepName    string
	spinner     *Spinner
	logger      Logger
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

func (sp *StepProgress) StartStep(stepName string) {
	sp.currentStep++
	sp.stepName = stepName
	
	// 如果有logger，抑制其控制台输出
	if sp.logger != nil {
		sp.logger.SuppressConsole()
		sp.logger.InfoToFileOnly("开始步骤 %d/%d: %s", sp.currentStep, sp.totalSteps, stepName)
	}
	
	prefix := fmt.Sprintf("[%d/%d]", sp.currentStep, sp.totalSteps)
	sp.spinner = NewSpinner(fmt.Sprintf("%s %s", prefix, stepName))
	sp.spinner.Start()
}

func (sp *StepProgress) UpdateStepProgress(message string) {
	if sp.spinner != nil {
		sp.spinner.UpdateSuffix(message)
	}
	
	// 记录详细进度到文件
	if sp.logger != nil {
		sp.logger.InfoToFileOnly("步骤进度更新: %s", message)
	}
}

func (sp *StepProgress) CompleteStep() {
	if sp.spinner != nil {
		sp.spinner.suffix = "完成"
		sp.spinner.Stop()
	}
	
	// 记录完成信息到文件
	if sp.logger != nil {
		sp.logger.InfoToFileOnly("步骤完成: %s", sp.stepName)
	}
}

func (sp *StepProgress) FailStep(errorMsg string) {
	if sp.spinner != nil {
		sp.spinner.suffix = fmt.Sprintf("失败: %s", errorMsg)
		sp.spinner.Fail()
	}
	
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