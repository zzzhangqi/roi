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

type StepProgress struct {
	totalSteps int
	currentStep int
	stepName string
	spinner *Spinner
}

func NewStepProgress(totalSteps int) *StepProgress {
	return &StepProgress{
		totalSteps: totalSteps,
		currentStep: 0,
	}
}

func (sp *StepProgress) StartStep(stepName string) {
	sp.currentStep++
	sp.stepName = stepName
	
	prefix := fmt.Sprintf("[%d/%d]", sp.currentStep, sp.totalSteps)
	sp.spinner = NewSpinner(fmt.Sprintf("%s %s", prefix, stepName))
	sp.spinner.Start()
}

func (sp *StepProgress) UpdateStepProgress(message string) {
	if sp.spinner != nil {
		sp.spinner.UpdateSuffix(message)
	}
}

func (sp *StepProgress) CompleteStep() {
	if sp.spinner != nil {
		sp.spinner.suffix = "完成"
		sp.spinner.Stop()
	}
}

func (sp *StepProgress) FailStep(errorMsg string) {
	if sp.spinner != nil {
		sp.spinner.suffix = fmt.Sprintf("失败: %s", errorMsg)
		sp.spinner.Fail()
	}
}